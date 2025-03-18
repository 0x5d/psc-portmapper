#!/bin/bash

set -e

TAG=${TAG:-0.0.0}

docker tag ko.local/github.com/0x5d/psc-portmapper 00x5d/psc-portmapper:${TAG}
docker push 00x5d/psc-portmapper:${TAG}
