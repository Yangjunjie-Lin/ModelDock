// Package secrets defines the small secret-manager boundary used by the
// runtime. Deployments can replace EnvManager with a Vault/KMS adapter without
// changing authentication, database, or provider code. Environment variables
// remain supported for backwards-compatible local deployments.
package secrets

import (
	"context"
	"errors"
	"os"
	"strings"
)

var ErrNotFound = errors.New("secret not found")

// Manager resolves a named secret without exposing how it is stored.
type Manager interface {
	Get(context.Context, string) (string, error)
}

// EnvManager is the compatibility provider for RELAYDOCK_* environment
// variables. Production should inject these values from its secret manager at
// process start rather than committing an environment file.
type EnvManager struct{}

func NewEnvManager() EnvManager { return EnvManager{} }

func (EnvManager) Get(_ context.Context, name string) (string, error) {
	value, ok := os.LookupEnv(strings.TrimSpace(name))
	if !ok || strings.TrimSpace(value) == "" {
		return "", ErrNotFound
	}
	return value, nil
}

// FileManager reads a secret from a runtime-mounted secret file. The path is
// resolved once per call so a CSI/ExternalSecrets rotation can be observed on
// the next request without retaining plaintext in process state.
type FileManager struct{ Root string }

func NewFileManager(root string) FileManager { return FileManager{Root: root} }

func (m FileManager) Get(_ context.Context, name string) (string, error) {
	if strings.TrimSpace(m.Root) == "" {
		return "", ErrNotFound
	}
	root, err := os.OpenRoot(m.Root)
	if err != nil {
		return "", ErrNotFound
	}
	defer root.Close()
	value, err := root.ReadFile(strings.TrimSpace(name))
	if err != nil || strings.TrimSpace(string(value)) == "" {
		return "", ErrNotFound
	}
	return strings.TrimSpace(string(value)), nil
}

// Require returns a secret or a stable error suitable for startup logs. The
// value itself is never included in the error.
func Require(ctx context.Context, manager Manager, name string) (string, error) {
	if manager == nil {
		return "", errors.New("secret manager is not configured")
	}
	value, err := manager.Get(ctx, name)
	if err != nil {
		return "", errors.New("required secret is unavailable: " + name)
	}
	return value, nil
}
