#!/bin/bash

set -e

make local

sudo cp fecore /usr/local/bin/ -f

sudo fecore install
