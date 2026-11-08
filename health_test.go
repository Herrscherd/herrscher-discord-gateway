package discord

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	contracts "github.com/Herrscherd/herrscher-contracts"
)

const probeTestToken = "probe-secret-token"

func botConfig() contracts.PluginConfig {
	return contracts.PluginConfig{Settings: map[string]string{botSettingKey: probeTestToken}}
}

func discordStub(t *testing.T, status int, body string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodGet {
			t.Errorf("probe sent %s, want a read-only GET", r.Method)
		}
		if r.URL.Path != currentUserPath {
			t.Errorf("probe hit %q, want %q", r.URL.Path, currentUserPath)
		}
		if got := r.Header.Get("Authorization"); got != "Bot "+probeTestToken {
			t.Errorf("Authorization = %q, want the bot token", got)
		}
		if got := r.Header.Get("User-Agent"); got != botUserAgent {
			t.Errorf("User-Agent = %q, want %q", got, botUserAgent)
		}
		w.WriteHeader(status)
		if _, err := io.WriteString(w, body); err != nil {
			t.Errorf("stub write: %v", err)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func probeAgainst(t *testing.T, baseURL string, access contracts.ProbeAccess) contracts.ProbeReport {
	t.Helper()
	return newHealthProbe(baseURL, http.DefaultClient)(t.Context(), access)
}

func argValue(t *testing.T, notice contracts.Notice, name string) string {
	t.Helper()
	for _, arg := range notice.Args {
		if arg.Name() == name {
			text, ok := arg.Value().(string)
			if !ok {
				t.Fatalf("argument %s of %v is not a text", name, notice.Code)
			}
			return text
		}
	}
	t.Fatalf("notice %v carries no %s argument", notice.Code, name)
	return ""
}

func TestProbeWithoutTokenIsOffAndAsksForTheVariable(t *testing.T) {
	srv, calls := discordStub(t, http.StatusOK, `{"username":"herrscher"}`)
	report := probeAgainst(t, srv.URL, contracts.ProbeAccess{Getenv: func(string) string { return "" }})
	if report.State != contracts.HealthOff {
		t.Fatalf("State = %v, want off", report.State)
	}
	if report.Detail.Code != contracts.CodeHealthEnvMissing || argValue(t, report.Detail, "Name") != botSettingEnv {
		t.Fatalf("Detail = %+v, want env missing naming %s", report.Detail, botSettingEnv)
	}
	if report.Remedy.Code != contracts.CodeHealthRemedyEnvRestart || argValue(t, report.Remedy, "Name") != botSettingEnv {
		t.Fatalf("Remedy = %+v, want an env restart naming %s", report.Remedy, botSettingEnv)
	}
	if calls.Load() != 0 {
		t.Fatalf("probe called Discord %d times without a token", calls.Load())
	}
}

func TestProbeWithNilGetenvAndNoTokenIsOff(t *testing.T) {
	report := newHealthProbe("http://127.0.0.1:1", http.DefaultClient)(t.Context(), contracts.ProbeAccess{})
	if report.State != contracts.HealthOff {
		t.Fatalf("State = %v, want off", report.State)
	}
}

func TestProbeReadsTheTokenFromTheEnvironmentWhenTheConfigHasNone(t *testing.T) {
	srv, _ := discordStub(t, http.StatusOK, `{"username":"herrscher"}`)
	getenv := func(key string) string {
		if key == botSettingEnv {
			return probeTestToken
		}
		return ""
	}
	report := probeAgainst(t, srv.URL, contracts.ProbeAccess{Getenv: getenv})
	if report.State != contracts.HealthOK {
		t.Fatalf("State = %v, want ok from the environment token", report.State)
	}
}

func TestProbeSignedInReportsTheBotNameAndLatency(t *testing.T) {
	srv, calls := discordStub(t, http.StatusOK, `{"id":"1","username":"herrscher","bot":true}`)
	report := probeAgainst(t, srv.URL, contracts.ProbeAccess{Config: botConfig()})
	if report.State != contracts.HealthOK {
		t.Fatalf("State = %v, want ok", report.State)
	}
	if report.Detail.Code != contracts.CodeHealthSignedInAs || argValue(t, report.Detail, "Name") != "herrscher" {
		t.Fatalf("Detail = %+v, want signed in as herrscher", report.Detail)
	}
	if report.Latency <= 0 {
		t.Fatalf("Latency = %v, want a measured round trip", report.Latency)
	}
	if calls.Load() != 1 {
		t.Fatalf("probe called Discord %d times, want one", calls.Load())
	}
}

func TestProbeMapsDiscordStatuses(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		state  contracts.HealthState
		code   contracts.Code
		reason string
	}{
		{"refused token", http.StatusUnauthorized, `{"message":"401: Unauthorized"}`, contracts.HealthDown, contracts.CodeHealthCredentialsRefused, ""},
		{"rate limited", http.StatusTooManyRequests, `{"retry_after":1}`, contracts.HealthDegraded, contracts.CodeHealthRateLimited, ""},
		{"server error", http.StatusServiceUnavailable, ``, contracts.HealthDegraded, contracts.CodeHealthUnreachable, "http 503"},
		{"garbled identity", http.StatusOK, `not json`, contracts.HealthDegraded, contracts.CodeHealthUnreachable, reasonBadResponse},
		{"nameless identity", http.StatusOK, `{"id":"1"}`, contracts.HealthDegraded, contracts.CodeHealthUnreachable, reasonNoIdentity},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, _ := discordStub(t, tc.status, tc.body)
			report := probeAgainst(t, srv.URL, contracts.ProbeAccess{Config: botConfig()})
			if report.State != tc.state || report.Detail.Code != tc.code {
				t.Fatalf("report = %+v, want %v with %v", report, tc.state, tc.code)
			}
			if tc.reason != "" && argValue(t, report.Detail, "Reason") != tc.reason {
				t.Fatalf("Reason = %q, want %q", argValue(t, report.Detail, "Reason"), tc.reason)
			}
		})
	}
}

func TestProbeUnreachableHostGivesAShortSafeReason(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	base := srv.URL
	srv.Close()
	report := probeAgainst(t, base, contracts.ProbeAccess{Config: botConfig()})
	if report.State != contracts.HealthDegraded || report.Detail.Code != contracts.CodeHealthUnreachable {
		t.Fatalf("report = %+v, want degraded unreachable", report)
	}
	reason := argValue(t, report.Detail, "Reason")
	if reason != reasonRefused {
		t.Fatalf("Reason = %q, want %q", reason, reasonRefused)
	}
	if strings.Contains(reason, probeTestToken) || strings.Contains(reason, base) {
		t.Fatalf("Reason %q leaks the token or the URL", reason)
	}
}

func TestProbeStopsAtTheContextDeadline(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-release }))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(release) })
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	report := newHealthProbe(srv.URL, http.DefaultClient)(ctx, contracts.ProbeAccess{Config: botConfig()})
	if report.State != contracts.HealthDegraded || argValue(t, report.Detail, "Reason") != reasonTimeout {
		t.Fatalf("report = %+v, want degraded with a timeout reason", report)
	}
}

func TestPluginRegistersItsProbe(t *testing.T) {
	for _, p := range contracts.Default.Gateways() {
		if p.Manifest.Kind == gatewayKind {
			if p.Probe == nil {
				t.Fatal("registered discord plugin has no Probe")
			}
			return
		}
	}
	t.Fatal("discord gateway did not self-register into contracts.Default")
}
