#!/bin/bash

set -e

make local

sudo cp core /usr/local/bin/ -f

sudo core install