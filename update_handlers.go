package main

import (
	"archive/zip"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
)

const (
	mbrPartitionName  = "mbr.img"
	bootPartitionName = "boot.img"
	rootPartitionName = "root.img"
)

type rcs struct {
	zip  *zip.ReadCloser
	mbr  io.ReadCloser
	boot io.ReadCloser
	root io.ReadCloser
}

// httpFetcher handles a http update link.
func httpFetcher(response *updateResponse, gusServer, destinationDir string) (rcs, error) {
	// The link may be a relative url if the server's backend registry is its local disk.
	// Ensure we have an absolute url by adding the base (gusServer) url
	// when necessary.
	link, err := ensureAbsoluteHTTPLink(gusServer, response.Link)
	if err != nil {
		return rcs{}, fmt.Errorf("error ensuring absolute HTTP link %q + %q: %w",
			gusServer, response.Link, err)
	}

	if err := os.MkdirAll(destinationDir, 0755); err != nil {
		return rcs{}, fmt.Errorf("error ensuring destination directory exists: %w", err)
	}

	log.Printf("downloading update file from registry %q with url: %s", response.RegistryType, link)

	filePath := filepath.Join(destinationDir, "disk.gaf")
	if err := httpDownloadFile(destinationDir, filePath, link); err != nil {
		return rcs{}, fmt.Errorf("unable to download update file: %w", err)
	}

	log.Print("loading disk partitions from update file")

	r, err := zip.OpenReader(filePath)
	if err != nil {
		return rcs{}, fmt.Errorf("error opening downloaded file %s: %w", filePath, err)
	}

	var mbrReader, bootReader, rootReader io.ReadCloser
	for _, f := range r.File {
		switch f.Name {
		case mbrPartitionName:
			mbrReader, err = f.Open()
			if err != nil {
				return rcs{}, fmt.Errorf("error reading %s within update file: %w", mbrPartitionName, err)
			}
		case bootPartitionName:
			bootReader, err = f.Open()
			if err != nil {
				return rcs{}, fmt.Errorf("error reading %s within update file: %w", bootPartitionName, err)
			}
		case rootPartitionName:
			rootReader, err = f.Open()
			if err != nil {
				return rcs{}, fmt.Errorf("error reading %s within update file: %w", rootPartitionName, err)
			}
		}
	}

	return rcs{r, mbrReader, bootReader, rootReader}, nil
}

// httpDownloadFile downloads a file from a url to a filepath on disk.
// It's efficient because it will write as it downloads
// and not load the whole file into memory.
func httpDownloadFile(destinationDir, filePath string, url string) error {
	availableDiskSpace, err := diskSpaceAvailable(destinationDir)
	if err != nil {
		return fmt.Errorf("error checking available disk space for %q", destinationDir)
	}

	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected HTTP status code: %v", resp.Status)
	}

	fileSize, err := strconv.Atoi(resp.Header.Get("Content-Length"))
	if err != nil {
		return fmt.Errorf("error while converting %d to string: %w", fileSize, err)
	}

	if availableDiskSpace < uint64(fileSize) {
		return fmt.Errorf("error: refused downloading file to %q,"+
			"not enough space left on the device. required: %d available %d",
			filePath, fileSize, availableDiskSpace)
	}

	// Create the file.
	out, err := os.Create(filePath)
	if err != nil {
		return err
	}

	// Write the body to file.
	if _, err := io.Copy(out, resp.Body); err != nil {
		return fmt.Errorf("error writing file %q to disk: %w", filePath, err)
	}

	if err := out.Close(); err != nil {
		return fmt.Errorf("error closing file %q: %w", filePath, err)
	}

	return nil
}

func ensureAbsoluteHTTPLink(baseURL string, link string) (string, error) {
	base, err := url.Parse(baseURL)
	if err != nil {
		return "", err
	}

	u, err := base.Parse(link)
	if err != nil {
		return "", err
	}

	return u.String(), nil
}

// Function to get available disk space for path.
func diskSpaceAvailable(path string) (uint64, error) {
	fs := syscall.Statfs_t{}
	err := syscall.Statfs(path, &fs)
	if err != nil {
		return 0, err
	}
	return fs.Bfree * uint64(fs.Bsize), nil
}
