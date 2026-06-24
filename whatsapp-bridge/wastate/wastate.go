package wastate

import "sync"

// State tracks the WhatsApp client's connection + login state, the
// current pairing QR PNG bytes (populated only while pairing is required),
// and the most recent WhatsApp Web client version string applied to the store.
type State struct {
	mu           sync.RWMutex
	connected    bool
	loggedIn     bool
	pairingQRPNG []byte
	pairingCode  string
	waVersion    string
}

func New() *State {
	return &State{}
}

func (s *State) Connected() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.connected
}

func (s *State) LoggedIn() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.loggedIn
}

// PairingQRPNG returns a copy of the current QR PNG bytes, or nil if
// pairing is not required. Returning a copy avoids aliasing issues if
// the state is updated concurrently.
func (s *State) PairingQRPNG() []byte {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.pairingQRPNG == nil {
		return nil
	}
	out := make([]byte, len(s.pairingQRPNG))
	copy(out, s.pairingQRPNG)
	return out
}

// PairingCode returns the raw pairing QR code string, or "" if pairing is not
// currently available. Clients can render this directly as a QR in a terminal.
func (s *State) PairingCode() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.pairingCode
}

// PairingRequired returns true when the client is not logged in AND
// a pairing QR is currently available.
func (s *State) PairingRequired() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return !s.loggedIn && s.pairingQRPNG != nil
}

func (s *State) SetConnected(v bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.connected = v
}

func (s *State) SetLoggedIn(v bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loggedIn = v
}

func (s *State) SetPairingQRPNG(b []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pairingQRPNG = b
}

// SetPairingCode stores the raw pairing QR code string.
func (s *State) SetPairingCode(code string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pairingCode = code
}

// ClearPairingQR clears both the QR PNG and the raw pairing code. Called once
// the client is logged in (or pairing is otherwise no longer in progress).
func (s *State) ClearPairingQR() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pairingQRPNG = nil
	s.pairingCode = ""
}

func (s *State) WAVersion() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.waVersion
}

func (s *State) SetWAVersion(v string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.waVersion = v
}
