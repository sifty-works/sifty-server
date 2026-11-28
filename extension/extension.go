package extension

import (
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
)

//go:embed manifest.json
var manifest []byte

func Origin() (string, error) {
	var m struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal(manifest, &m); err != nil {
		return "", fmt.Errorf("extension manifest: %w", err)
	}
	der, err := base64.StdEncoding.DecodeString(m.Key)
	if err != nil || len(der) == 0 {
		return "", fmt.Errorf("extension manifest: the key is not base64")
	}
	sum := sha256.Sum256(der)
	id := make([]byte, 0, 32)
	for _, b := range sum[:16] {
		id = append(id, 'a'+b>>4, 'a'+b&15)
	}
	return "chrome-extension://" + string(id), nil
}
