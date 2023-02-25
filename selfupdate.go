package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"

	"github.com/gokrazy/updater"
)

type updateRequest struct {
	MachineID string `json:"machine_id"`
	SBOMHash  string `json:"sbom_hash"`
}

type attemptRequest struct {
	MachineID string `json:"machine_id"`
	SBOMHash  string `json:"sbom_hash"`
}

type updateResponse struct {
	SBOMHash     string `json:"sbom_hash"`
	RegistryType string `json:"registry_type"`
	Link         string `json:"download_link"`
}

const (
	updateAPI        = "api/v1/update"
	attemptUpdateAPI = "api/v1/attempt"
)

func registerUpdateAttempt(ctx context.Context, gusServer string, request *attemptRequest) error {
	reqBody, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("error json encoding request: %w", err)
	}

	attemptEndpoint, err := url.JoinPath(gusServer, attemptUpdateAPI)
	if err != nil {
		return fmt.Errorf("error joining attempt endpoint: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", attemptEndpoint, bytes.NewBuffer(reqBody))
	if err != nil {
		return fmt.Errorf("error creating http request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("error making http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected HTTP status code: %v", resp.Status)
	}
	return nil
}

func checkForUpdates(ctx context.Context, gusServer string, request *updateRequest) (*updateResponse, error) {
	reqBody, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("error json encoding request: %w", err)
	}

	updateEndpoint, err := url.JoinPath(gusServer, updateAPI)
	if err != nil {
		return nil, fmt.Errorf("error joining update endpoint: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", updateEndpoint, bytes.NewBuffer(reqBody))
	if err != nil {
		return nil, fmt.Errorf("error creating http request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("error making http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected HTTP status code: %v", resp.Status)
	}

	respBody, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("error reading http response: %w", err)
	}

	var response updateResponse
	if err := json.Unmarshal(respBody, &response); err != nil {
		return nil, fmt.Errorf("error json decoding response: %w", err)
	}

	return &response, nil
}

func shouldUpdate(response *updateResponse, sbomHash string) bool {
	if response.SBOMHash == sbomHash {
		log.Printf("device's gokrazy version: %s is already the desired one, skipping", response.SBOMHash)
		return false
	}

	log.Printf("device's gokrazy version: %s, desired version: %s, proceeding with the update", sbomHash, response.SBOMHash)

	return true
}

func selfupdate(ctx context.Context, gusServer, destinationDir string, request *updateRequest, response *updateResponse, httpPassword, httpPort string) error {
	log.Print("starting self-update procedure")

	attemptReq := attemptRequest{MachineID: request.MachineID, SBOMHash: response.SBOMHash}
	if err := registerUpdateAttempt(ctx, gusServer, &attemptReq); err != nil {
		return fmt.Errorf("error registering update attempt to %q: %w", gusServer, err)
	}

	var readClosers rcs
	var err error

	switch response.RegistryType {
	case "http", "localdisk":
		readClosers, err = httpFetcher(response, gusServer, destinationDir)
		if err != nil {
			return fmt.Errorf("error fetching %q update from link %q: %w", response.RegistryType, response.Link, err)
		}
	default:
		return fmt.Errorf("unrecognized registry type %q", response.RegistryType)
	}

	uri := fmt.Sprintf("http://gokrazy:%s@localhost:%s/", httpPassword, httpPort)

	log.Print("checking target partuuid support")

	target, err := updater.NewTarget(uri, http.DefaultClient)
	if err != nil {
		return fmt.Errorf("checking target partuuid support: %v", err)
	}

	// Start with the root file system because writing to the non-active
	// partition cannot break the currently running system.
	log.Print("updating root file system")
	if err := target.StreamTo("root", readClosers.root); err != nil {
		return fmt.Errorf("updating root file system: %v", err)
	}
	readClosers.root.Close()

	log.Print("updating boot file system")
	if err := target.StreamTo("boot", readClosers.boot); err != nil {
		return fmt.Errorf("updating boot file system: %v", err)
	}
	readClosers.boot.Close()

	// Only relevant when running on non-Raspberry Pi devices.
	// As it does not use an MBR.
	log.Print("updating MBR")
	if err := target.StreamTo("mbr", readClosers.mbr); err != nil {
		return fmt.Errorf("updating MBR: %v", err)
	}
	readClosers.mbr.Close()

	readClosers.zip.Close()

	log.Print("switching to non-active partition")
	if err := target.Switch(); err != nil {
		return fmt.Errorf("switching to non-active partition: %v", err)
	}

	log.Print("reboot")
	if err := target.Reboot(); err != nil {
		return fmt.Errorf("reboot: %v", err)
	}

	return nil
}
