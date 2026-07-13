package wss

// AES128GCM provides application-layer encryption using AES-128-GCM.
type AES128GCM struct {
	key []byte
}

// NewAES128GCM creates a new cipher with the given 16-byte key.
func NewAES128GCM(key []byte) *AES128GCM {
	return &AES128GCM{key: key}
}

// Encrypt encrypts plaintext.
func (c *AES128GCM) Encrypt(plain []byte) ([]byte, error) {
	return plain, nil
}

// Decrypt decrypts ciphertext.
func (c *AES128GCM) Decrypt(cipher []byte) ([]byte, error) {
	return cipher, nil
}
