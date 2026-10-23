package setupauth

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

type Token struct {
	Token     string    `json:"token"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
	Used      bool      `json:"used"`
}

type Store struct {
	path string
	mu   sync.Mutex
}

func NewStore(dataDir string) Store {
	return Store{path: filepath.Join(dataDir, "setup-token.json")}
}

func (s *Store) Create(ttl time.Duration) (Token, error) {
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	token := Token{Token: randomToken(32), CreatedAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(ttl), Used: false}
	if err := s.save(token); err != nil {
		return Token{}, err
	}
	return token, nil
}

func (s *Store) Load() (Token, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := os.ReadFile(s.path)
	if err != nil {
		return Token{}, err
	}
	var token Token
	if err := json.Unmarshal(b, &token); err != nil {
		return Token{}, err
	}
	return token, nil
}

func (s *Store) Validate(value string) (Token, error) {
	token, err := s.Load()
	if err != nil {
		return Token{}, err
	}
	if token.Token == "" || value == "" || token.Token != value {
		return Token{}, errors.New("invalid setup token")
	}
	if token.Used {
		return Token{}, errors.New("setup token has already been used")
	}
	if time.Now().UTC().After(token.ExpiresAt) {
		return Token{}, errors.New("setup token has expired")
	}
	return token, nil
}

func (s *Store) Consume(value string) (Token, error) {
	token, err := s.Validate(value)
	if err != nil {
		return Token{}, err
	}
	token.Used = true
	if err := s.save(token); err != nil {
		return Token{}, err
	}
	return token, nil
}

func (s *Store) save(token Token) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(s.path), 0o750); err != nil {
		return err
	}
	b, err := json.MarshalIndent(token, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(s.path, b, 0o640); err != nil {
		return err
	}
	if cloudriftsUser, err := user.Lookup("cloudrifts"); err == nil {
		uid, uidErr := strconv.Atoi(cloudriftsUser.Uid)
		gid, gidErr := strconv.Atoi(cloudriftsUser.Gid)
		if uidErr == nil && gidErr == nil {
			_ = os.Chown(s.path, uid, gid)
		}
	}
	return nil
}

func randomToken(bytesLen int) string {
	b := make([]byte, bytesLen)
	if _, err := rand.Read(b); err != nil {
		return hex.EncodeToString([]byte(time.Now().UTC().Format(time.RFC3339Nano)))
	}
	return hex.EncodeToString(b)
}
