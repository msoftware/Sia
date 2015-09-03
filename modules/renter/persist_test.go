package renter

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/NebulousLabs/Sia/crypto"
	"github.com/NebulousLabs/Sia/encoding"
)

// newTestingFile initializes a file object with random parameters.
func newTestingFile() *file {
	key, _ := crypto.GenerateTwofishKey()
	data, _ := crypto.RandBytes(14)
	nData, _ := crypto.RandIntn(10)
	nParity, _ := crypto.RandIntn(10)
	rsc, _ := NewRSCode(nData+1, nParity+1)

	return &file{
		name:        "testfile-" + strconv.Itoa(int(data[0])),
		size:        encoding.DecUint64(data[1:5]),
		masterKey:   key,
		erasureCode: rsc,
		pieceSize:   encoding.DecUint64(data[6:8]),

		bytesUploaded:  encoding.DecUint64(data[9:11]),
		chunksUploaded: encoding.DecUint64(data[12:14]),
	}
}

// equalFiles is a helper function that compares two files for equality.
func equalFiles(f1, f2 *file) error {
	if f1 == nil || f2 == nil {
		return fmt.Errorf("one or both files are nil")
	}
	if f1.name != f2.name {
		return fmt.Errorf("names do not match: %v %v", f1.name, f2.name)
	}
	if f1.size != f2.size {
		return fmt.Errorf("sizes do not match: %v %v", f1.size, f2.size)
	}
	if f1.masterKey != f2.masterKey {
		return fmt.Errorf("keys do not match: %v %v", f1.masterKey, f2.masterKey)
	}
	if f1.pieceSize != f2.pieceSize {
		return fmt.Errorf("pieceSizes do not match: %v %v", f1.pieceSize, f2.pieceSize)
	}
	if f1.bytesUploaded != f2.bytesUploaded {
		return fmt.Errorf("bytesUploaded does not match: %v %v", f1.bytesUploaded, f2.bytesUploaded)
	}
	if f1.chunksUploaded != f2.chunksUploaded {
		return fmt.Errorf("chunksUploaded does not match: %v %v", f1.chunksUploaded, f2.chunksUploaded)
	}
	return nil
}

// TestFileSaveLoad tests the save and load functions of the file type.
func TestFileSaveLoad(t *testing.T) {
	savedFile := newTestingFile()
	buf := new(bytes.Buffer)
	savedFile.save(buf)

	loadedFile := new(file)
	err := loadedFile.load(buf)
	if err != nil {
		t.Fatal(err)
	}

	err = equalFiles(savedFile, loadedFile)
	if err != nil {
		t.Fatal(err)
	}
}

// TestFileSaveLoadASCII tests the ASCII saving/loading functions.
func TestFileSaveLoadASCII(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	rt, err := newRenterTester("TestRenterSaveLoad")
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()

	// Create a file and add it to the renter.
	savedFile := newTestingFile()
	rt.renter.files[savedFile.name] = savedFile

	ascii, err := rt.renter.ShareFilesAscii([]string{savedFile.name})
	if err != nil {
		t.Fatal(err)
	}

	// Remove the file from the renter.
	delete(rt.renter.files, savedFile.name)

	names, err := rt.renter.LoadSharedFilesAscii(ascii)
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 1 || names[0] != savedFile.name {
		t.Fatal("nickname not loaded properly")
	}

	err = equalFiles(rt.renter.files[savedFile.name], savedFile)
	if err != nil {
		t.Fatal(err)
	}
}

// TestRenterSaveLoad probes the save and load methods of the renter type.
func TestRenterSaveLoad(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	rt, err := newRenterTester("TestRenterSaveLoad")
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()

	// Create and save some files
	var f1, f2, f3 *file
	f1 = newTestingFile()
	f2 = newTestingFile()
	f3 = newTestingFile()
	// names must not conflict
	for f2.name == f1.name || f2.name == f3.name {
		f2 = newTestingFile()
	}
	for f3.name == f1.name || f3.name == f2.name {
		f3 = newTestingFile()
	}
	rt.renter.saveFile(f1)
	rt.renter.saveFile(f2)
	rt.renter.saveFile(f3)

	// load should now load the files into memory.
	err = rt.renter.load()
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}

	if err := equalFiles(f1, rt.renter.files[f1.name]); err != nil {
		t.Fatal(err)
	}
	if err := equalFiles(f2, rt.renter.files[f2.name]); err != nil {
		t.Fatal(err)
	}
	if err := equalFiles(f3, rt.renter.files[f3.name]); err != nil {
		t.Fatal(err)
	}

	// Corrupt a renter file and try to reload it.
	err = ioutil.WriteFile(filepath.Join(rt.renter.saveDir, "corrupt"+ShareExtension), []byte{1, 2, 3}, 0660)
	if err != nil {
		t.Fatal(err)
	}

	err = rt.renter.load()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}
