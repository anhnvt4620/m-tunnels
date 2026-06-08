package dashboard

import (
	"crypto/rand"
	_ "embed"
	"encoding/base64"
)

//go:embed ui.html
var dashboardHTML string

func generateToken() string {
	b := make([]byte, 36)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}
