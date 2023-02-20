.PHONY: deps gus instance update check cleanup e2e

export GUS_SERVER = http://10.0.2.2:8655
export GOKRAZY_PORT = 8181
export GOKRAZY_PASSWORD = TTTEEESSSTTT

deps:
	go install github.com/gokrazy/tools/cmd/gok@main
	go install github.com/gokrazy/gus/cmd/...@main
	go install github.com/damdo/gokrazy-machine/cmd/...@main

gus:
	mkdir -p /tmp/imagedir-e2e
	gus-server -listen ":8655" -image_dir /tmp/imagedir-e2e &>/dev/null &

instance:
	rm -rf $$HOME/gokrazy/hello
	gok -i hello new
	gok -i hello add github.com/gokrazy/selfupdate
	WORKDIR=$$(pwd) && cd $$HOME/gokrazy/hello/builddir/github.com/gokrazy/selfupdate && go mod edit -replace "github.com/gokrazy/selfupdate=$$WORKDIR" && cd $$WORKDIR
	jq ".PackageConfig += {\"github.com/gokrazy/selfupdate\":{\"CommandLineFlags\":[\"--gus_server=$$GUS_SERVER\",\"--check_frequency=20s\",\"--skip_waiting=true\"]}}" $${HOME}/gokrazy/hello/config.json > INPUT.tmp && mv INPUT.tmp $${HOME}/gokrazy/hello/config.json
	jq ".PackageConfig += {\"github.com/gokrazy/gokrazy/cmd/heartbeat\":{\"CommandLineFlags\":[\"--gus_server=$$GUS_SERVER\",\"--frequency=2s\"]}}" $${HOME}/gokrazy/hello/config.json > INPUT.tmp && mv INPUT.tmp $${HOME}/gokrazy/hello/config.json
	jq ".Update.HTTPPassword = \"$${GOKRAZY_PASSWORD}\"" $${HOME}/gokrazy/hello/config.json > INPUT.tmp && mv INPUT.tmp $${HOME}/gokrazy/hello/config.json
	GOARCH=arm64 gok -i hello overwrite --full /tmp/disk-e2e.img --target_storage_bytes=2147483648
	gom play --arch arm64 --full /tmp/disk-e2e.img --net-nat="$${GOKRAZY_PORT}-:80,2222-:22" &>/dev/null &

update:
	gok -i hello add github.com/gokrazy/timestamps
	gok -i hello overwrite --gaf /tmp/disk-e2e.gaf
	export SBOM=$$(unzip -p /tmp/disk-e2e.gaf sbom.json | jq -r '.sbom_hash') && \
	export LINK=$$(gok -i hello push --gaf /tmp/disk-e2e.gaf --server http://localhost:8655 --json | jq -r '.download_link') && \
	export MACHINEID=$$(cat $$HOME/gokrazy/hello/config.json | jq -r '.PackageConfig."github.com/gokrazy/gokrazy/cmd/randomd".ExtraFileContents."/etc/machine-id"' | xargs) && \
	curl -sL -d "{\"machine_id_pattern\": \"$${MACHINEID}\", \"sbom_hash\": \"$${SBOM}\" , \"registry_type\": \"localdisk\", \"download_link\": \"$${LINK}\" }" -X POST http://localhost:8655/api/v1/ingest

.SILENT:
check:
	export MACHINEID=$$(cat $$HOME/gokrazy/hello/config.json | jq -r '.PackageConfig."github.com/gokrazy/gokrazy/cmd/randomd".ExtraFileContents."/etc/machine-id"' | xargs) && \
	start=$$(date +%s); \
	end=$$((start + 300)); \
	while [ $$(date +%s) -lt $$end ]; \
	do \
	  DESIRED_SBOM=$$(curl -sL -H 'Accept: application/json' -d "{\"machine_id\":\"$${MACHINEID}\"}" http://localhost:8655/api/v1/update | jq -r '.sbom_hash'); \
	  CURRENT_SBOM=$$(curl -sL -H 'Accept: application/json' "http://gokrazy:$${GOKRAZY_PASSWORD}@localhost:$${GOKRAZY_PORT}/" | jq -r '.SBOMHash'); \
	  if [ "$$DESIRED_SBOM" == "$$CURRENT_SBOM" ];\
	  then \
	    echo "success";\
	    exit 0;\
	  fi;\
	  sleep 5;\
	done;\
	echo "SBOMs did not become equal within 5 minutes, update failed";\
	exit 1;

cleanup:
	pkill gus-server
	pkill gom

e2e: deps gus instance update check cleanup
