package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gokrazy/gokrazy"
)

type opts struct {
	gusServer      string
	checkFrequency string
	destinationDir string
	skipWaiting    bool
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	log.Print("gokrazy's selfupdate service starting up..")

	var o opts

	flag.StringVar(&o.gusServer, "gus_server", "", "the HTTP/S endpoint of the GUS (gokrazy Update System) server (required)")
	flag.StringVar(&o.checkFrequency, "check_frequency", "1h", "the time frequency for checks to the update service. default: 1h")
	flag.StringVar(&o.destinationDir, "destination_dir", "/tmp/selfupdate", "the destination directory for the fetched update file. default: /tmp/selfupdate")
	flag.BoolVar(&o.skipWaiting, "skip_waiting", false, "skips the time frequency check and jitter waits, and immediately performs an update check. default: false")

	flag.Parse()

	if err := logic(ctx, o); err != nil {
		log.Fatal(err)
	}
}

func logic(ctx context.Context, o opts) error {
	if o.gusServer == "" {
		return fmt.Errorf("flag --gus_server must be provided")
	}

	frequency, err := time.ParseDuration(o.checkFrequency)
	if err != nil {
		return fmt.Errorf("failed to parse check_frequency duration: %w", err)
	}

	machineID := gokrazy.MachineID()

	_, sbomHash, err := gokrazy.ReadSBOM()
	if err != nil {
		return fmt.Errorf("could not read SBOM from disk: %s", err.Error())
	}

	httpPassword, err := readConfigFile("gokr-pw.txt")
	if err != nil {
		return fmt.Errorf("could read neither /perm/gokr-pw.txt, nor /etc/gokr-pw.txt, nor /gokr-pw.txt: %s", err.Error())
	}

	httpPort, err := readConfigFile("http-port.txt")
	if err != nil {
		return fmt.Errorf("could read neither /perm/http-port.txt, nor /etc/http-port.txt, nor /http-port.txt: %s", err.Error())
	}

	if o.skipWaiting {
		log.Print("skipping waiting, performing an immediate updateProcess")
		if err := updateProcess(ctx, &updateRequest{MachineID: machineID, SBOMHash: sbomHash}, o.gusServer, sbomHash, o.destinationDir, httpPassword, httpPort); err != nil {
			// If the updateProcess fails we exit with an error
			// so that gokrazy supervisor will restart the process.
			return fmt.Errorf("error performing updateProcess: %v", err)
		}

		// If the updateProcess doesn't error
		// we happily return to terminate the process.
		return nil
	}

	log.Print("entering update checking loop")
	ticker := time.NewTicker(frequency)

	for {
		select {
		case <-ctx.Done():
			log.Print("stopping update checking")
			return nil

		case <-ticker.C:
			jitter := time.Duration(rand.Int63n(250)) * time.Second
			time.Sleep(jitter)
			if err := updateProcess(ctx, &updateRequest{MachineID: machineID, SBOMHash: sbomHash}, o.gusServer, sbomHash, o.destinationDir, httpPassword, httpPort); err != nil {
				log.Printf("error performing updateProcess: %v", err)
				continue
			}
		}
	}
}

func updateProcess(ctx context.Context, upReq *updateRequest, gusServer, sbomHash, destinationDir, httpPassword, httpPort string) error {
	response, err := checkForUpdates(ctx, gusServer, upReq)
	if err != nil {
		return fmt.Errorf("unable to check for updates: %w", err)
	}

	// Check if we should update by comparing the update response SBOMHash with
	// the current installation SBOMHash.
	if !shouldUpdate(response, sbomHash) {
		return nil
	}

	// The SBOMHash differs, start the selfupdate procedure.
	if err := selfupdate(ctx, gusServer, destinationDir, upReq, response, httpPassword, httpPort); err != nil {
		return fmt.Errorf("unable to perform the selfupdate procedure: %w", err)
	}

	// The update is now correctly written to the disk partitions
	// and the reboot is in progress
	// sleep until the context chan is closed, then exit cleanly.
	<-ctx.Done()
	os.Exit(0)

	return nil
}
