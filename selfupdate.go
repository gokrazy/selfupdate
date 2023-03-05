package main

import (
	"context"
	"fmt"
	"log"
	"net/http"

	"github.com/antihax/optional"
	"github.com/gokrazy/gokapi/gusapi"
	"github.com/gokrazy/updater"
)

func checkForUpdates(ctx context.Context, gusCli *gusapi.APIClient, machineID string) (gusapi.UpdateResponse, error) {
	response, _, err := gusCli.UpdateApi.Update(ctx, &gusapi.UpdateApiUpdateOpts{
		Body: optional.NewInterface(&gusapi.UpdateRequest{
			MachineId: machineID,
		}),
	})
	if err != nil {
		return gusapi.UpdateResponse{}, fmt.Errorf("error making http request: %w", err)
	}

	return response, nil
}

func shouldUpdate(response gusapi.UpdateResponse, sbomHash string) bool {
	if response.SbomHash == sbomHash {
		log.Printf("device's gokrazy version: %s is already the desired one, skipping", response.SbomHash)
		return false
	}

	log.Printf("device's gokrazy version: %s, desired version: %s, proceeding with the update", sbomHash, response.SbomHash)

	return true
}

func selfupdate(ctx context.Context, gusCli *gusapi.APIClient, gusServer, machineID, destinationDir string, response gusapi.UpdateResponse, httpPassword, httpPort string) error {
	log.Print("starting self-update procedure")

	if _, _, err := gusCli.UpdateApi.Attempt(ctx, &gusapi.UpdateApiAttemptOpts{
		Body: optional.NewInterface(&gusapi.AttemptRequest{
			MachineId: machineID,
			SbomHash:  response.SbomHash,
		}),
	}); err != nil {
		return fmt.Errorf("error registering update attempt: %w", err)
	}

	var readClosers rcs
	var err error

	switch response.RegistryType {
	case "http", "localdisk":
		readClosers, err = httpFetcher(response, gusServer, destinationDir)
		if err != nil {
			return fmt.Errorf("error fetching %q update from link %q: %w", response.RegistryType, response.DownloadLink, err)
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
