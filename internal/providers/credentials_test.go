package providers

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestStoreCredentialFallsBackToFileWhenKeyringUnavailable(t *testing.T) {
	origGet := keyringGet
	origSet := keyringSet
	origHome := userHomeDir
	defer func() {
		keyringGet = origGet
		keyringSet = origSet
		userHomeDir = origHome
	}()

	tmpHome := t.TempDir()
	userHomeDir = func() (string, error) { return tmpHome, nil }
	keyringSet = func(service, user, password string) error { return errors.New("keyring unavailable") }
	keyringGet = func(service, user string) (string, error) { return "", errors.New("keyring unavailable") }

	if err := StoreCredential("openai", "sk-test"); err != nil {
		t.Fatalf("store credential: %v", err)
	}

	credentialPath := filepath.Join(tmpHome, ".config", "orchestra", "credentials.json")
	info, err := os.Stat(credentialPath)
	if err != nil {
		t.Fatalf("stat credential file: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("expected credential file mode 0600, got %o", got)
	}

	got, err := LoadCredential("openai")
	if err != nil {
		t.Fatalf("load credential: %v", err)
	}
	if got != "sk-test" {
		t.Fatalf("expected stored credential, got %q", got)
	}
}

func TestStoreCredentialUsesKeyringWhenAvailable(t *testing.T) {
	origGet := keyringGet
	origSet := keyringSet
	origHome := userHomeDir
	defer func() {
		keyringGet = origGet
		keyringSet = origSet
		userHomeDir = origHome
	}()

	tmpHome := t.TempDir()
	userHomeDir = func() (string, error) { return tmpHome, nil }

	keyringValues := make(map[string]string)
	keyringSet = func(service, user, password string) error {
		keyringValues[user] = password
		return nil
	}
	keyringGet = func(service, user string) (string, error) {
		value := keyringValues[user]
		if value == "" {
			return "", errors.New("not found")
		}
		return value, nil
	}

	if err := StoreCredential("anthropic", "sk-ant"); err != nil {
		t.Fatalf("store credential: %v", err)
	}
	if got := keyringValues["anthropic"]; got != "sk-ant" {
		t.Fatalf("expected keyring value persisted, got %q", got)
	}
	credentialPath := filepath.Join(tmpHome, ".config", "orchestra", "credentials.json")
	if _, err := os.Stat(credentialPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected no credential fallback file when keyring succeeds, got err=%v", err)
	}
}
