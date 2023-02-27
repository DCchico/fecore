#!/bin/bash

set -e

make local

sudo cp bin/fecore /usr/local/bin/ -f

sudo fecore install
