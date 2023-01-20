#!/usr/bin/env bash
# Runs the tool against all images in images.txt
set -e
# this helps cleanup by cleanup.sh
prefix="hasmodfiles-run"

for image in $(cat images.txt); do
    normalizedImage=$(echo "${image}" | tr [:punct:] _)
    dirname="${prefix}-${normalizedImage}"
    mkdir "${dirname}"
    pushd "${dirname}" &>/dev/null
    go run ../. "${image}"
    popd &>/dev/null
    echo "--"
done