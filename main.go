package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"github.com/gokrazy/gokapi/gusapi"
	"github.com/gokrazy/gokrazy"
)

type opts struct {
	gusServer      string
	checkFrequency string
	destinationDir string
	skipWaiting    bool

	plugin     string
	pluginArgs []string
}

type plugin struct {
	binPath string
	name    string
	args    []string
}

const (
	serverAPIPath = "/api/v1"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	log.Print("gokrazy's selfupdate service starting up..")

	gokrazy.WaitForClock()

	var o opts

	flag.StringVar(&o.gusServer, "gus_server", "", "the HTTP/S endpoint of the GUS (gokrazy Update System) server (required)")
	flag.StringVar(&o.checkFrequency, "check_frequency", "1h", "the time frequency for checks to the update service. default: 1h")
	flag.StringVar(&o.destinationDir, "destination_dir", "/tmp/selfupdate", "the destination directory for the fetched update file. default: /tmp/selfupdate")
	flag.BoolVar(&o.skipWaiting, "skip_waiting", false, "for the first update check it skips the time frequency check and jitter, useful for testing. default: false")
	flag.StringVar(&o.plugin, "plugin", "", "name of the desired plugin to be loaded (this will be used when needed). default: ''")

	flag.Parse()

	// Gather args after flag parsing termination "--".
	// They will be directly passed to the plugin binary.
	o.pluginArgs = flag.Args()

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

	gusBasePath, err := url.JoinPath(o.gusServer, serverAPIPath)
	if err != nil {
		return fmt.Errorf("error joining gus server url: %w", err)
	}

	plugins := make(map[string]plugin)
	if err := loadPlugin(plugins, o.plugin, o.pluginArgs); err != nil {
		return fmt.Errorf("error loading plugin %s: %w", o.plugin, err)
	}

	gusCfg := gusapi.NewConfiguration()
	gusCfg.BasePath = gusBasePath
	gusCli := gusapi.NewAPIClient(gusCfg)

	if o.skipWaiting {
		log.Print("skipping waiting, performing an immediate update check")
		if err := updateProcess(ctx, gusCli, plugins, machineID, o.gusServer, sbomHash, o.destinationDir, httpPassword, httpPort); err != nil {
			// If the updateProcess fails we exit with an error
			// so that gokrazy supervisor will restart the process.
			return fmt.Errorf("error performing updateProcess: %v", err)
		}
	}

	for c := time.Tick(frequency); ; {
		select {
		case <-ctx.Done():
			log.Print("shutting down...")
			return nil
		case <-c:
			if o.skipWaiting {
				// Re-introduce jitter after first run skip.
				o.skipWaiting = false
				jitter := time.Duration(rand.Int63n(250)) * time.Second
				time.Sleep(jitter)
			}

			if err := updateProcess(ctx, gusCli, plugins, machineID, o.gusServer, sbomHash, o.destinationDir, httpPassword, httpPort); err != nil {
				log.Printf("error performing updateProcess: %v", err)
				continue
			}
		}
	}
}

func updateProcess(ctx context.Context, gusCli *gusapi.APIClient, plugins map[string]plugin, machineID, gusServer, sbomHash, destinationDir, httpPassword, httpPort string) error {
	response, err := checkForUpdates(ctx, gusCli, machineID)
	if err != nil {
		return fmt.Errorf("unable to check for updates: %w", err)
	}

	// Check if we should update by comparing the update response SBOMHash with
	// the current installation SBOMHash.
	if !shouldUpdate(response, sbomHash) {
		return nil
	}

	// The SBOMHash differs, start the selfupdate procedure.
	if err := selfupdate(ctx, gusCli, plugins, gusServer, machineID, destinationDir, response, httpPassword, httpPort); err != nil {
		return fmt.Errorf("unable to perform the selfupdate procedure: %w", err)
	}

	// The update is now correctly written to the disk partitions
	// and the reboot is in progress
	// sleep until the context chan is closed, then exit cleanly.
	<-ctx.Done()
	os.Exit(0)

	return nil
}

func loadPlugin(plugins map[string]plugin, pluginName string, pluginArgs []string) error {
	var binPath string

	// Try to find the plugin binary in PATH.
	fullPluginName := fmt.Sprintf("gokplugin-%s", pluginName)
	if p, err := exec.LookPath(fullPluginName); err == nil {
		binPath = p
	} else {
		// The binary can't be found in PATH.
		// Fall back to checking in the well known gokrazy's /user/ path.
		fallbackPath := fmt.Sprintf("/user/%s", fullPluginName)
		if _, err := os.Stat(fallbackPath); errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("unable to find %s", fullPluginName)
		}
		binPath = fallbackPath
	}
	plugins[pluginName] = plugin{binPath: binPath, name: pluginName, args: pluginArgs}

	return nil
}
