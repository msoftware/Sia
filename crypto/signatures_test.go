package crypto

import (
	"bytes"
	"crypto/rand"
	"errors"
	"testing"

	"github.com/NebulousLabs/Sia/encoding"
	"github.com/NebulousLabs/ed25519"
)

// mockKeyDeriver is a mock implementation of keyDeriver that saves its provided
// entropy and allows the client to specify the returned SecretKey and
// PublicKey.
type mockKeyDeriver struct {
	called  bool
	entropy [EntropySize]byte
	sk      ed25519.SecretKey
	pk      ed25519.PublicKey
}

func (kd *mockKeyDeriver) deriveKeyPair(entropy [EntropySize]byte) (ed25519.SecretKey, ed25519.PublicKey) {
	kd.called = true
	kd.entropy = entropy
	return kd.sk, kd.pk
}

// Test that the Generate method is properly calling its dependencies and
// returning the expected key pair.
func TestGenerateRandomKeyPair(t *testing.T) {
	var mockEntropy [EntropySize]byte
	mockEntropy[0] = 5
	mockEntropy[EntropySize-1] = 5
	entropyReader := bytes.NewReader(mockEntropy[:])

	sk := ed25519.SecretKey(&[SecretKeySize]byte{})
	sk[0] = 7
	sk[32] = 8
	pk := ed25519.PublicKey(&[PublicKeySize]byte{})
	pk[0] = sk[32]
	kd := mockKeyDeriver{sk: sk, pk: pk}

	// Create a SignatureKeyGenerator using mocks.
	g := stdGenerator{entropyReader, &kd}

	// Create key pair.
	skActual, pkActual, err := g.Generate()

	// Verify that we got back the expected results.
	if err != nil {
		t.Error(err)
	}
	if *sk != skActual {
		t.Errorf("Generated secret key does not match expected! expected = %v, actual = %v", sk, skActual)
	}
	if *pk != pkActual {
		t.Errorf("Generated public key does not match expected! expected = %v, actual = %v", pk, pkActual)
	}

	// Verify the dependencies were called correctly
	if !kd.called {
		t.Error("keyDeriver was never called.")
	}
	if mockEntropy != kd.entropy {
		t.Errorf("keyDeriver was called with the wrong entropy. expected = %v, actual = %v", mockEntropy, kd.entropy)
	}
}

// failingReader is a mock implementation of io.Reader that fails with a client-
// defined error.
type failingReader struct {
	err error
}

func (fr failingReader) Read([]byte) (int, error) {
	return 0, fr.err
}

// Test that the Generate method fails if the call to entropy source fails
func TestGenerateRandomKeyPairFailsWhenRandFails(t *testing.T) {
	fr := failingReader{err: errors.New("mock error from entropy reader")}
	g := stdGenerator{entropySource: &fr}
	if _, _, err := g.Generate(); err == nil {
		t.Error("Generate should fail when entropy source fails.")
	}
}

// Test that the GenerateDeterministic method is properly calling its
// dependencies and returning the expected key pair.
func TestGenerateDeterministicKeyPair(t *testing.T) {
	// Create entropy bytes, setting a few bytes explicitly instead of using a
	// buffer of random bytes.
	var mockEntropy [EntropySize]byte
	mockEntropy[0] = 4
	mockEntropy[EntropySize-1] = 5

	sk := ed25519.SecretKey(&[SecretKeySize]byte{})
	sk[0] = 7
	sk[32] = 8
	pk := ed25519.PublicKey(&[PublicKeySize]byte{})
	pk[0] = sk[32]
	kd := mockKeyDeriver{sk: sk, pk: pk}
	g := stdGenerator{kd: &kd}

	// Create key pair.
	skActual, pkActual := g.GenerateDeterministic(mockEntropy)

	// Verify that we got back the right results.
	if *sk != skActual {
		t.Errorf("Generated secret key does not match expected! expected = %v, actual = %v", sk, skActual)
	}
	if *pk != pkActual {
		t.Errorf("Generated public key does not match expected! expected = %v, actual = %v", pk, pkActual)
	}

	// Verify the dependencies were called correctly.
	if !kd.called {
		t.Error("keyDeriver was never called.")
	}
	if mockEntropy != kd.entropy {
		t.Errorf("keyDeriver was called with the wrong entropy. expected = %v, actual = %v", mockEntropy, kd.entropy)
	}
}

// Creates and encodes a public key, and verifies that it decodes correctly,
// does the same with a signature.
func TestSignatureEncoding(t *testing.T) {
	// Create a dummy key pair.
	var sk SecretKey
	sk[0] = 4
	sk[32] = 5
	pk := sk.PublicKey()

	// Marshal and unmarshal the public key.
	marshalledPK := encoding.Marshal(pk)
	var unmarshalledPK PublicKey
	err := encoding.Unmarshal(marshalledPK, &unmarshalledPK)
	if err != nil {
		t.Fatal(err)
	}

	// Test the public keys for equality.
	if pk != unmarshalledPK {
		t.Error("pubkey not the same after marshalling and unmarshalling")
	}

	// Create a signature using the secret key.
	var signedData Hash
	rand.Read(signedData[:])
	sig, err := SignHash(signedData, sk)
	if err != nil {
		t.Fatal(err)
	}

	// Marshal and unmarshal the signature.
	marshalledSig := encoding.Marshal(sig)
	var unmarshalledSig Signature
	err = encoding.Unmarshal(marshalledSig, &unmarshalledSig)
	if err != nil {
		t.Fatal(err)
	}

	// Test signatures for equality.
	if sig != unmarshalledSig {
		t.Error("signature not same after marshalling and unmarshalling")
	}

}

// TestSigning creates a bunch of keypairs and signs random data with each of
// them.
func TestSigning(t *testing.T) {
	var iterations int
	if testing.Short() {
		t.SkipNow()
	}

	// Try a bunch of signatures because at one point there was a library that
	// worked around 98% of the time. Tests would usually pass, but 200
	// iterations would normally cause a failure.
	iterations = 200
	for i := 0; i < iterations; i++ {
		// Create dummy key pair.
		var entropy [EntropySize]byte
		entropy[0] = 5
		entropy[1] = 8
		sk, pk := StdKeyGen.GenerateDeterministic(entropy)

		// Generate and sign the data.
		var randData Hash
		rand.Read(randData[:])
		sig, err := SignHash(randData, sk)
		if err != nil {
			t.Fatal(err)
		}

		// Verify the signature.
		err = VerifyHash(randData, pk, sig)
		if err != nil {
			t.Fatal(err)
		}

		// Attempt to verify after the data has been altered.
		randData[0] += 1
		err = VerifyHash(randData, pk, sig)
		if err != ErrInvalidSignature {
			t.Fatal(err)
		}

		// Restore the data and make sure the signature is valid again.
		randData[0] -= 1
		err = VerifyHash(randData, pk, sig)
		if err != nil {
			t.Fatal(err)
		}

		// Attempt to verify after the signature has been altered.
		sig[0] += 1
		err = VerifyHash(randData, pk, sig)
		if err != ErrInvalidSignature {
			t.Fatal(err)
		}
	}
}
