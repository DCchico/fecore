#!/bin/bash
# run with sudo

set -e

systemctl stop core
systemctl stop core-provider

GO=/usr/local/go/bin/go
$GO build

cp core /usr/local/bin/ -f

systemctl start core
systemctl start core-provider