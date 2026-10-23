package cloudinit

import (
	"fmt"
	"os"
	"path/filepath"

	diskfs "github.com/diskfs/go-diskfs"
	"github.com/diskfs/go-diskfs/disk"
	"github.com/diskfs/go-diskfs/filesystem"
	"github.com/diskfs/go-diskfs/filesystem/iso9660"
)

const isoSize int64 = 10 * 1024 * 1024

// MetaData is the NoCloud meta-data file.
func MetaData(instanceID, hostname string) []byte {
	return []byte("instance-id: " + instanceID + "\nlocal-hostname: " + hostname + "\n")
}

// NetworkConfig is a netplan v2 file that DHCPs the NIC with this MAC.
// mac is the lowercase colon form stored on the HyperVMachine.
func NetworkConfig(mac string) []byte {
	return []byte("version: 2\nethernets:\n  id0:\n    match:\n      macaddress: \"" + mac + "\"\n    dhcp4: true\n    dhcp6: false\n")
}

// Build writes a Rock Ridge cidata ISO containing user-data, meta-data, and network-config.
func Build(userData, metaData, networkConfig []byte) ([]byte, error) {
	dir, err := os.MkdirTemp("", "hyperv-cidata-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "cidata.iso")
	image, err := diskfs.Create(path, isoSize, diskfs.SectorSizeDefault)
	if err != nil {
		return nil, fmt.Errorf("create iso: %w", err)
	}
	image.LogicalBlocksize = 2048
	fs, err := image.CreateFilesystem(disk.FilesystemSpec{
		Partition:   0,
		FSType:      filesystem.TypeISO9660,
		VolumeLabel: "cidata",
	})
	if err != nil {
		return nil, fmt.Errorf("create iso filesystem: %w", err)
	}
	files := []struct {
		name    string
		content []byte
	}{
		{name: "user-data", content: userData},
		{name: "meta-data", content: metaData},
		{name: "network-config", content: networkConfig},
	}
	for _, file := range files {
		if err := writeISOFile(fs, file.name, file.content); err != nil {
			_ = fs.Close()
			return nil, err
		}
	}
	iso, ok := fs.(*iso9660.FileSystem)
	if !ok {
		_ = fs.Close()
		return nil, fmt.Errorf("iso filesystem has type %T", fs)
	}
	if err := iso.Finalize(iso9660.FinalizeOptions{
		RockRidge:        true,
		VolumeIdentifier: "cidata",
	}); err != nil {
		_ = fs.Close()
		return nil, fmt.Errorf("finalize iso: %w", err)
	}
	if err := fs.Close(); err != nil {
		return nil, fmt.Errorf("close iso: %w", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return data, nil
}

func writeISOFile(fs filesystem.FileSystem, name string, content []byte) error {
	f, err := fs.OpenFile(name, os.O_CREATE|os.O_RDWR)
	if err != nil {
		return fmt.Errorf("create %s: %w", name, err)
	}
	if _, err := f.Write(content); err != nil {
		_ = f.Close()
		return fmt.Errorf("write %s: %w", name, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close %s: %w", name, err)
	}
	return nil
}
