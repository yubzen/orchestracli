package providers

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/zalando/go-keyring"
)

const credentialService = "orchestra"

var ErrCredentialNotFound = errors.New("credential not found")

var (
	credentialFileMu sync.Mutex
	keyringGet       = keyring.Get
	keyringSet       = keyring.Set
	userHomeDir      = os.UserHomeDir
)

func StoreCredential(keyName, key string) error {
	keyName = strings.TrimSpace(keyName)
	key = strings.TrimSpace(key)
	if keyName == "" {
		return errors.New("credential key name is empty")
	}
	if err := ValidateCredential(key); err != nil {
		return err
	}

	if err := keyringSet(credentialService, keyName, key); err == nil {
		return nil
	}

	credentialFileMu.Lock()
	defer credentialFileMu.Unlock()

	entries, err := readCredentialFileUnlocked()
	if err != nil {
		return err
	}
	entries[keyName] = key
	return writeCredentialFileUnlocked(entries)
}

func LoadCredential(keyName string) (string, error) {
	keyName = strings.TrimSpace(keyName)
	if keyName == "" {
		return "", errors.New("credential key name is empty")
	}

	if key, err := keyringGet(credentialService, keyName); err == nil {
		key = strings.TrimSpace(key)
		if key != "" {
			return key, nil
		}
	}

	credentialFileMu.Lock()
	defer credentialFileMu.Unlock()

	entries, err := readCredentialFileUnlocked()
	if err != nil {
		return "", err
	}
	key := strings.TrimSpace(entries[keyName])
	if key == "" {
		return "", ErrCredentialNotFound
	}
	return key, nil
}

func credentialFilePath() (string, error) {
	home, err := userHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	home = strings.TrimSpace(home)
	if home == "" {
		return "", errors.New("home directory is empty")
	}
	return filepath.Join(home, ".config", "orchestra", "credentials.json"), nil
}

func readCredentialFileUnlocked() (map[string]string, error) {
	path, err := credentialFilePath()
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read credential file: %w", err)
	}
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return map[string]string{}, nil
	}
	entries := make(map[string]string)
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, fmt.Errorf("decode credential file: %w", err)
	}
	clean := make(map[string]string, len(entries))
	for k, v := range entries {
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if k == "" || v == "" {
			continue
		}
		clean[k] = v
	}
	return clean, nil
}

func writeCredentialFileUnlocked(entries map[string]string) error {
	path, err := credentialFilePath()
	if err != nil {
		return err
	}
	if entries == nil {
		entries = map[string]string{}
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create credential directory: %w", err)
	}
	payload, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return fmt.Errorf("encode credential file: %w", err)
	}
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, payload, 0o600); err != nil {
		return fmt.Errorf("write credential temp file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("replace credential file: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("set credential file permissions: %w", err)
	}
	return nil
}
