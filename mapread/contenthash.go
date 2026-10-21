package mapread

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// ContentHash is the SHA-256, in hex, of what doc publishes as data: its
// version, attribution, anime and entries. GeneratedAt and Sources do not
// change it, so a rebuild that changes no row keeps its hash.
func ContentHash(doc *Document) (string, error) {
	b, err := json.Marshal(struct {
		Anime       []Anime     `json:"anime"`
		Attribution Attribution `json:"attribution"`
		Entries     []Entry     `json:"entries"`
		Version     int         `json:"version"`
	}{doc.Anime, doc.Attribution, doc.Entries, doc.Version})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}
