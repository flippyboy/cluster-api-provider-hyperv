package hyperv

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

func TestSignerFromKey(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatal(err)
	}
	signer, err := signerFromKey(pem.EncodeToMemory(block))
	if err != nil {
		t.Fatal(err)
	}
	if signer.PublicKey().Type() == "" {
		t.Fatal("empty key type")
	}
}

func TestSignerRejectsEncryptedKey(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKeyWithPassphrase(priv, "", []byte("pass"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = signerFromKey(pem.EncodeToMemory(block))
	if err == nil || !strings.Contains(err.Error(), "encrypted") {
		t.Fatal(err)
	}
}

func TestHostKeyCallbackRequiresKnownHosts(t *testing.T) {
	if _, err := hostKeyCallback(nil, false); err == nil {
		t.Fatal("expected error")
	}
	cb, err := hostKeyCallback(nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if cb == nil {
		t.Fatal("nil callback")
	}
}

func TestSFTPPath(t *testing.T) {
	if got := sftpPath(`C:\VMs\vm\cidata.iso`); got != "C:/VMs/vm/cidata.iso" {
		t.Fatal(got)
	}
}
