package webpush

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"testing"
)

func TestVAPIDPrivateKeyValidation(t *testing.T) {
	original, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := original.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	key, err := vapidPrivateKeyToECDSA(raw)
	if err != nil || !original.Equal(key) {
		t.Fatal("VAPID key did not preserve private/public binding", err)
	}
	for _, invalid := range []string{"missing", "short", "long", "zero", "out of range"} {
		t.Run(invalid, func(t *testing.T) {
			var scalar []byte
			switch invalid {
			case "short":
				scalar = raw[1:]
			case "long":
				scalar = append([]byte{0}, raw...)
			case "zero":
				scalar = make([]byte, 32)
			case "out of range":
				scalar = elliptic.P256().Params().N.Bytes()
			}
			if _, err := vapidPrivateKeyToECDSA(scalar); err == nil {
				t.Fatal("invalid VAPID scalar accepted")
			}
		})
	}
}
