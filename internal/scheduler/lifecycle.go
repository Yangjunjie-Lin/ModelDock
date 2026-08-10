package scheduler

import "time"

type CredentialTransition struct {
	Status        string
	CooldownUntil *time.Time
	MarkSuccess   bool
}

// TransitionForHTTP centralizes the small, auditable credential lifecycle.
// Only a definitive 401 permanently excludes a credential. A provider 429
// creates a bounded cooldown; unrelated client or provider errors do not
// poison the credential.
func TransitionForHTTP(status int, now time.Time, cooldown time.Duration) CredentialTransition {
	switch {
	case status == 401:
		return CredentialTransition{Status: "AUTH_FAILED"}
	case status == 429:
		until := now.UTC().Add(cooldown)
		return CredentialTransition{Status: "COOLDOWN", CooldownUntil: &until}
	case status >= 200 && status < 400:
		return CredentialTransition{Status: "ACTIVE", MarkSuccess: true}
	default:
		return CredentialTransition{}
	}
}
