package hashutil

import (
	"crypto/sha256"
	"encoding/hex"
)

func SHA256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
