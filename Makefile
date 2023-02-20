.PHONY: deps gus instance update check cleanup e2e

SHELL=/bin/bash

export GUS_SERVER_HOST = http://10.0.2.2
export GUS_SERVER_PORT = 8655
export GOKRAZY_PORT = 8181
export GOKRAZY_PASSWORD = TTTEEESSSTTT

setup:
	echo ">>>> performing e2e test setup.."
	rm -f "/tmp/logs/*"
	mkdir -p /tmp/logs

deps:
	echo ">>>> installing dependencies.."
	go install github.com/gokrazy/tools/cmd/gok@main >> /tmp/logs/test-deps-setup.txt 2>&1
	go install github.com/gokrazy/gus/cmd/...@main >> /tmp/logs/test-deps-setup.txt 2>&1
	go install github.com/damdo/gokrazy-machine/cmd/...@main >> /tmp/logs/test-deps-setup.txt 2>&1

gus:
	echo ">>>> setting up GUS server.."
	mkdir -p /tmp/imagedir-e2e
	gus-server -listen ":8655" -image_dir /tmp/imagedir-e2e 2>&1 >/tmp/logs/gus-server.txt &

instance:
	echo ">>>> building and launching gokrazy instance.."
	rm -rf $$HOME/gokrazy/hello
	gok -i hello new
	gok -i hello add github.com/gokrazy/selfupdate
	WORKDIR=$$(pwd) && cd $$HOME/gokrazy/hello/builddir/github.com/gokrazy/selfupdate && go mod edit -replace "github.com/gokrazy/selfupdate=$$WORKDIR" && cd $$WORKDIR
	jq ".PackageConfig += {\"github.com/gokrazy/selfupdate\":{\"CommandLineFlags\":[\"--gus_server=$$GUS_SERVER_HOST:$$GUS_SERVER_PORT\",\"--check_frequency=5s\",\"--skip_waiting=true\"]}}" $${HOME}/gokrazy/hello/config.json > INPUT.tmp && mv INPUT.tmp $${HOME}/gokrazy/hello/config.json
	jq ".PackageConfig += {\"github.com/gokrazy/gokrazy/cmd/heartbeat\":{\"CommandLineFlags\":[\"--gus_server=$$GUS_SERVER_HOST:$$GUS_SERVER_PORT\",\"--frequency=2s\"]}}" $${HOME}/gokrazy/hello/config.json > INPUT.tmp && mv INPUT.tmp $${HOME}/gokrazy/hello/config.json
	jq ".Update.HTTPPassword = \"$${GOKRAZY_PASSWORD}\"" $${HOME}/gokrazy/hello/config.json > INPUT.tmp && mv INPUT.tmp $${HOME}/gokrazy/hello/config.json
	GOARCH=arm64 gok -i hello overwrite --full /tmp/disk-e2e.img --target_storage_bytes=2147483648 2>&1 >/tmp/logs/gokrazy-first-build.txt
	gom play --arch arm64 --full /tmp/disk-e2e.img --net-nat="$${GOKRAZY_PORT}-:80,2222-:22" 2>&1 >/tmp/logs/gom.txt &

update:
	echo ">>>> building and creating and setting up a gokrazy instance update.."
	gok -i hello add github.com/gokrazy/timestamps 2>&1 >/tmp/logs/gokrazy-update-build.txt
	gok -i hello overwrite --gaf /tmp/disk-e2e.gaf 2>&1 >/tmp/logs/gokrazy-update-build.txt
	export SBOM=$$(unzip -p /tmp/disk-e2e.gaf sbom.json | jq -r '.sbom_hash') && \
	export LINK=$$(gok -i hello push --gaf /tmp/disk-e2e.gaf --server http://localhost:8655 --json | jq -r '.download_link') && \
	export MACHINEID=$$(cat $$HOME/gokrazy/hello/config.json | jq -r '.PackageConfig."github.com/gokrazy/gokrazy/cmd/randomd".ExtraFileContents."/etc/machine-id"' | xargs) && \
	curl -sL -d "{\"machine_id_pattern\": \"$${MACHINEID}\", \"sbom_hash\": \"$${SBOM}\" , \"registry_type\": \"localdisk\", \"download_link\": \"$${LINK}\" }" -X POST http://localhost:8655/api/v1/ingest

.SILENT:
check:
	echo ">>>> checking for a successful selfupdate of the gokrazy instance.."
	export MACHINEID=$$(cat $$HOME/gokrazy/hello/config.json | jq -r '.PackageConfig."github.com/gokrazy/gokrazy/cmd/randomd".ExtraFileContents."/etc/machine-id"' | xargs) && \
	echo $$MACHINEID && \
	start=$$(date +%s) && \
	end=$$((start + 300)) && \
	while [ $$(date +%s) -lt $$end ]; \
	do \
	  DESIRED_SBOM=$$(curl -sL -H 'Accept: application/json' -d "{\"machine_id\":\"$${MACHINEID}\"}" http://localhost:8655/api/v1/update | jq -r '.sbom_hash'); \
	  CURRENT_SBOM=$$(curl -sL -H 'Accept: application/json' "http://gokrazy:$${GOKRAZY_PASSWORD}@localhost:$${GOKRAZY_PORT}/" | jq -r '.SBOMHash'); \
		echo "" && \
		echo "current version: $$CURRENT_SBOM" && \
		echo "desired version: $$DESIRED_SBOM" && \
	  if [ "$$DESIRED_SBOM" = "$$CURRENT_SBOM" ]; then \
	    echo "current and desired version match, success!"; \
	    exit 0; \
	  fi; \
	  sleep 5; \
	done; \
	echo "version did not become equal within 5 minutes, considering update as failed"; \
	exit 1;

cleanup:
	echo ">>>> cleanup.."
	pkill gus-server
	pkill gom

e2e: setup deps gus instance update check cleanup

test:
	docker build -t testimg -f Dockerfile.test .
	docker run --rm -it -p $${GOKRAZY_PORT} -p $${GUS_SERVER_PORT} -v="$${PWD}/logs:/tmp/logs" testimg /bin/bash -c 'make e2e'

