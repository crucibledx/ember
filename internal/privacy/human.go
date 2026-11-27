package privacy

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

func hashHumanList(key []byte, value string) string {
	ids := strings.Split(value, ",")
	for i, id := range ids {
		if id != "" && !strings.HasPrefix(id, "h1:") {
			ids[i] = hashHuman(key, id)
		}
	}
	return strings.Join(ids, ",")
}

func hashHuman(key []byte, id string) string {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(id))
	return "h1:" + hex.EncodeToString(mac.Sum(nil)[:8])
}
