package discord

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"syscall"
	"time"

	contracts "github.com/Herrscherd/herrscher-contracts"
)

const (
	botSettingKey     = "token"
	botSettingEnv     = "DISCORD_BOT_TOKEN"
	discordAPIBase    = "https://discord.com/api/v10"
	currentUserPath   = "/users/@me"
	currentUserMaxLen = 64 << 10
)

const (
	reasonTimeout      = "timeout"
	reasonCanceled     = "canceled"
	reasonDNS          = "dns lookup failed"
	reasonRefused      = "connection refused"
	reasonNetwork      = "network error"
	reasonBadRequest   = "invalid request"
	reasonBadResponse  = "invalid response"
	reasonNoIdentity   = "no bot name in response"
	reasonStatusPrefix = "http "
)

type botIdentity struct {
	Username string `json:"username"`
}

type healthProbe struct {
	baseURL string
	client  *http.Client
}

func newHealthProbe(baseURL string, client *http.Client) contracts.HealthProber {
	probe := healthProbe{baseURL: baseURL, client: client}
	return probe.check
}

func (p healthProbe) check(ctx context.Context, access contracts.ProbeAccess) contracts.ProbeReport {
	token := probeToken(access)
	if token == "" {
		return contracts.ProbeReport{
			State:  contracts.HealthOff,
			Detail: contracts.Notice{Code: contracts.CodeHealthEnvMissing, Args: []contracts.Arg{contracts.Text("Name", botSettingEnv)}},
			Remedy: contracts.Notice{Code: contracts.CodeHealthRemedyEnvRestart, Args: []contracts.Arg{contracts.Text("Name", botSettingEnv)}},
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+currentUserPath, http.NoBody)
	if err != nil {
		return unreachable(reasonBadRequest, 0)
	}
	req.Header.Set("Authorization", "Bot "+token)
	req.Header.Set("Accept", "application/json")
	start := time.Now()
	resp, err := p.client.Do(req)
	if err != nil {
		return unreachable(transportReason(ctx, err), 0)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, currentUserMaxLen))
	err = errors.Join(err, resp.Body.Close())
	latency := time.Since(start)
	if err != nil {
		return unreachable(transportReason(ctx, err), latency)
	}
	return reportStatus(resp.StatusCode, body, latency)
}

func probeToken(access contracts.ProbeAccess) string {
	if token := access.Config.Get(botSettingKey); token != "" {
		return token
	}
	if access.Getenv == nil {
		return ""
	}
	return access.Getenv(botSettingEnv)
}

func reportStatus(status int, body []byte, latency time.Duration) contracts.ProbeReport {
	switch status {
	case http.StatusOK:
		return signedIn(body, latency)
	case http.StatusUnauthorized:
		return contracts.ProbeReport{
			State:   contracts.HealthDown,
			Latency: latency,
			Detail:  contracts.Notice{Code: contracts.CodeHealthCredentialsRefused},
		}
	case http.StatusTooManyRequests:
		return contracts.ProbeReport{
			State:   contracts.HealthDegraded,
			Latency: latency,
			Detail:  contracts.Notice{Code: contracts.CodeHealthRateLimited},
		}
	}
	return unreachable(fmt.Sprintf("%s%d", reasonStatusPrefix, status), latency)
}

func signedIn(body []byte, latency time.Duration) contracts.ProbeReport {
	var identity botIdentity
	if err := json.Unmarshal(body, &identity); err != nil {
		return unreachable(reasonBadResponse, latency)
	}
	if identity.Username == "" {
		return unreachable(reasonNoIdentity, latency)
	}
	return contracts.ProbeReport{
		State:   contracts.HealthOK,
		Latency: latency,
		Detail:  contracts.Notice{Code: contracts.CodeHealthSignedInAs, Args: []contracts.Arg{contracts.Text("Name", identity.Username)}},
	}
}

func unreachable(reason string, latency time.Duration) contracts.ProbeReport {
	return contracts.ProbeReport{
		State:   contracts.HealthDegraded,
		Latency: latency,
		Detail:  contracts.Notice{Code: contracts.CodeHealthUnreachable, Args: []contracts.Arg{contracts.Text("Reason", reason)}},
	}
}

func transportReason(ctx context.Context, err error) string {
	var netErr net.Error
	var dnsErr *net.DNSError
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded), errors.Is(err, context.DeadlineExceeded):
		return reasonTimeout
	case errors.Is(ctx.Err(), context.Canceled), errors.Is(err, context.Canceled):
		return reasonCanceled
	case errors.As(err, &dnsErr):
		return reasonDNS
	case errors.Is(err, syscall.ECONNREFUSED):
		return reasonRefused
	case errors.As(err, &netErr) && netErr.Timeout():
		return reasonTimeout
	}
	return reasonNetwork
}
