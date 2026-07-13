// Package security implements the federated blacklist/whitelist
// policy model and the four risk modes (Strict/Normal/Trust/YOLO).
package security

// Mode represents a risk mode.
type Mode int

const (
	ModeStrict Mode = iota
	ModeNormal
	ModeTrust
	ModeYOLO
)

// Policy holds the merged rules from server and client.
type Policy struct {
	Mode Mode
}

// New creates a default policy.
func New() *Policy {
	return &Policy{Mode: ModeNormal}
}
