#!/bin/bash
set -e # Exits on error

NAME=$(date "+%Y_%m_%d")
OSID=$(uname)

if [ "$OSID" = "Darwin" ]; then
{
for i in linux-amd64 linux-arm64; do
    make ARCH=$i all
    done
mkdir -p build
zip -qr -FS ./build/"tunasync-$NAME-linux-amd64.zip" ./build-linux-amd64/*
zip -qr -FS ./build/"tunasync-$NAME-linux-arm64.zip" ./build-linux-arm64/*
}
else
if [ "$OSID" = "Linux" ]; then
{
for i in linux-amd64 linux-arm64; do
    make ARCH=$i all
    done
mkdir -p build
zip -qr -FS ./build/"tunasync-$NAME-linux-amd64.zip" ./build-linux-amd64/*
zip -qr -FS ./build/"tunasync-$NAME-linux-arm64.zip" ./build-linux-arm64/*
    }
fi
fi

exit 0
