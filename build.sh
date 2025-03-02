#!/bin/bash

ko build --platform linux/amd64 --platform linux/arm64 --local --preserve-import-paths --tag-only github.com/0x5d/psc-portmapper
