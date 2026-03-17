package network

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"
)

// Credential is a bearer token issued to a specific remote client identity.
type Credential struct {
	ID        string     `json:"id"`
	TokenHash string     `json:"token_hash"`
	CreatedAt time.Time  `json:"created_at"`
	Revoked   bool       `json:"revoked"`
	RevokedAt *time.Time `json:"revoked_at,omitempty"`
}

// CredentialStore holds credentials in memory and persists them to a JSON file.
// All methods are safe for concurrent use.
type CredentialStore struct {
	mu    sync.RWMutex
	creds map[string]*Credential // keyed by token hash
}

// NewCredentialStore creates an empty CredentialStore.
func NewCredentialStore() *CredentialStore {
	return &CredentialStore{
		creds: make(map[string]*Credential),
	}
}

// Add inserts a new credential. The token is hashed before storage.
// Returns an error if the id already exists.
func (s *CredentialStore) Add(id, token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, c := range s.creds {
		if c.ID == id {
			return fmt.Errorf("credential already exists")
		}
	}

	hash := hashToken(token)
	s.creds[hash] = &Credential{
		ID:        id,
		TokenHash: hash,
		CreatedAt: time.Now().UTC(),
		Revoked:   false,
	}
	return nil
}

// Validate checks whether token is a valid, non-revoked credential.
// Returns (true, credential) on success, (false, nil) otherwise.
func (s *CredentialStore) Validate(token string) (bool, *Credential) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	hash := hashToken(token)
	c, ok := s.creds[hash]
	if !ok || c.Revoked {
		return false, nil
	}
	return true, c
}

// Revoke marks the credential with the given id as revoked.
func (s *CredentialStore) Revoke(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, c := range s.creds {
		if c.ID == id {
			if c.Revoked {
				return nil // idempotent
			}
			now := time.Now().UTC()
			c.Revoked = true
			c.RevokedAt = &now
			return nil
		}
	}
	return fmt.Errorf("credential not found")
}

// List returns a snapshot of all credentials.
func (s *CredentialStore) List() []*Credential {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]*Credential, 0, len(s.creds))
	for _, c := range s.creds {
		cp := *c
		out = append(out, &cp)
	}
	return out
}

// diskCredential is the on-disk representation (token_hash only, never plaintext).
type diskCredential struct {
	ID        string     `json:"id"`
	TokenHash string     `json:"token_hash"`
	CreatedAt time.Time  `json:"created_at"`
	Revoked   bool       `json:"revoked"`
	RevokedAt *time.Time `json:"revoked_at,omitempty"`
}

// LoadFromFile reads credentials from a JSON file. If the file does not
// exist the store is left empty without error (first-run behaviour).
func (s *CredentialStore) LoadFromFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read credentials: %w", err)
	}

	var disk []diskCredential
	if err := json.Unmarshal(data, &disk); err != nil {
		return fmt.Errorf("parse credentials: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for _, d := range disk {
		c := &Credential{
			ID:        d.ID,
			TokenHash: d.TokenHash,
			CreatedAt: d.CreatedAt,
			Revoked:   d.Revoked,
			RevokedAt: d.RevokedAt,
		}
		s.creds[d.TokenHash] = c
	}
	return nil
}

// SaveToFile persists all credentials to a JSON file (token_hash only).
func (s *CredentialStore) SaveToFile(path string) error {
	s.mu.RLock()
	disk := make([]diskCredential, 0, len(s.creds))
	for _, c := range s.creds {
		disk = append(disk, diskCredential{
			ID:        c.ID,
			TokenHash: c.TokenHash,
			CreatedAt: c.CreatedAt,
			Revoked:   c.Revoked,
			RevokedAt: c.RevokedAt,
		})
	}
	s.mu.RUnlock()

	data, err := json.MarshalIndent(disk, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal credentials: %w", err)
	}
	return os.WriteFile(path, data, 0o600)
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
