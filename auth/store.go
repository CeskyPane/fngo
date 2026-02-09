package auth

import (
	"context"
	"sync"
)

type TokenStore interface {
	Load(ctx context.Context) (TokenSet, bool, error)
	Save(ctx context.Context, tokens TokenSet) error
	Clear(ctx context.Context) error
}

type DeviceAuthStore interface {
	Load(ctx context.Context, accountID string) (DeviceAuth, bool, error)
	Save(ctx context.Context, creds DeviceAuth) error
	Clear(ctx context.Context, accountID string) error
}

type MemoryTokenStore struct {
	mu    sync.RWMutex
	valid bool
	token TokenSet
}

func NewMemoryTokenStore() *MemoryTokenStore {
	return &MemoryTokenStore{}
}

func (s *MemoryTokenStore) Load(_ context.Context) (TokenSet, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if !s.valid {
		return TokenSet{}, false, nil
	}

	return s.token.Clone(), true, nil
}

func (s *MemoryTokenStore) Save(_ context.Context, tokens TokenSet) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.token = tokens.Clone()
	s.valid = true

	return nil
}

func (s *MemoryTokenStore) Clear(_ context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.token = TokenSet{}
	s.valid = false

	return nil
}

type MemoryDeviceAuthStore struct {
	mu   sync.RWMutex
	data map[string]DeviceAuth
}

func NewMemoryDeviceAuthStore() *MemoryDeviceAuthStore {
	return &MemoryDeviceAuthStore{data: make(map[string]DeviceAuth)}
}

func (s *MemoryDeviceAuthStore) Load(_ context.Context, accountID string) (DeviceAuth, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	v, ok := s.data[accountID]
	if !ok {
		return DeviceAuth{}, false, nil
	}

	return v, true, nil
}

func (s *MemoryDeviceAuthStore) Save(_ context.Context, creds DeviceAuth) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.data[creds.AccountID] = creds

	return nil
}

func (s *MemoryDeviceAuthStore) Clear(_ context.Context, accountID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.data, accountID)

	return nil
}
