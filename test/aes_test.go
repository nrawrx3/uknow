package test

import (
	"testing"

	"github.com/nrawrx3/uknow"
)

func TestEncryptWithAES(t *testing.T) {
	hexKey := "55cfb2bd7e7803532bfcc3ca9f08c3e601e68b26d98fd4119dacace2ab668ce3"

	plaintext := []byte("Scar tissue that I wished you saw")

	aesCipher, err := uknow.NewAESCipher(hexKey)
	if err != nil {
		t.Log(err)
		t.Fail()
	}

	encryptedBytes, err := aesCipher.Encrypt(plaintext)
	if err != nil {
		t.Log(err)
		t.FailNow()
	}

	decryptedBytes, err := aesCipher.Decrypt(encryptedBytes)
	if err != nil {
		t.Log(err)
		t.FailNow()
	}

	if string(decryptedBytes) != string(plaintext) {
		t.Log("decryptedBytes != plainText")
		t.Logf("decryptedBytes = %s", string(decryptedBytes))
		t.Fail()
	}
}
