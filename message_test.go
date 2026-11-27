package discord

import "testing"

func TestOnlyDefaultAndReplyMessagesAreConversational(t *testing.T) {
	t.Parallel()
	cases := []struct {
		kind messageType
		want bool
	}{
		{kind: 0, want: true},
		{kind: 19, want: true},
		{kind: 7, want: false},
		{kind: 18, want: false},
		{kind: 20, want: false},
		{kind: 21, want: false},
		{kind: 23, want: false},
	}
	for _, tc := range cases {
		if got := (messageCreate{Type: tc.kind}).conversational(); got != tc.want {
			t.Errorf("type %d conversational = %v, want %v", tc.kind, got, tc.want)
		}
	}
}
