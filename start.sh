#!/bin/bash

set -e

sudo systemctl stop core
sudo systemctl stop core-provider

GO=/usr/local/go/bin/go
$GO build

cp core /usr/local/bin/ -f

sudo systemctl start core
sudo systemctl start core-provider