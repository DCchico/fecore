#!/bin/bash
# run with sudo

set -e

# systemctl stop fecore
# systemctl stop fecore-provider

GO=/usr/local/go/bin/go
$GO build

cp fecore /usr/local/bin/ -f

# systemctl start fecore
# systemctl start fecore-provider
