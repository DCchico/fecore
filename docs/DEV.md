## Instructions for building and testing core locally

## HACKY - use core instead of faasd if found

> Note: if you're just wanting to try out core, then it's likely that you're on the wrong page. This is a detailed set of instructions for those wanting to contribute or customise core. Feel free to go back to the homepage and pick a tutorial instead.

Do you want to help the community test a pull request?

See these instructions instead: [Testing patches](/docs/PATCHES.md)

### Pre-reqs

> It's recommended that you do not install Docker on the same host as core, since 1) they may both use different versions of containerd and 2) docker's networking rules can disrupt core's networking. When using core - make your core server a core server, and build container image on your laptop or in a CI pipeline.

* Linux

    PC / Cloud - any Linux that containerd works on should be fair game, but core is tested with Ubuntu 18.04

    For Raspberry Pi Raspbian Stretch or newer also works fine

    For MacOS users try [multipass.run](https://multipass.run) or [Vagrant](https://www.vagrantup.com/)

    For Windows users, install [Git Bash](https://git-scm.com/downloads) along with multipass or vagrant. You can also use WSL1 or WSL2 which provides a Linux environment.

    You will also need [containerd](https://github.com/containerd/containerd) and the [CNI plugins](https://github.com/containernetworking/plugins)

    [faas-cli](https://github.com/openfaas/faas-cli) is optional, but recommended.


### Get runc

```bash
sudo apt update \
  && sudo apt install -qy \
    runc \
    bridge-utils \
    make
```

### Get faas-cli (optional)

Having `faas-cli` on your dev machine is useful for testing and debug.

```bash
curl -sLS https://cli.openfaas.com | sudo sh
```

#### Install the CNI plugins:

* For PC run `export ARCH=amd64`
* For RPi/armhf run `export ARCH=arm`
* For arm64 run `export ARCH=arm64`

Then run:

```bash
export ARCH=amd64
export CNI_VERSION=v0.9.1

sudo mkdir -p /opt/cni/bin
curl -sSL https://github.com/containernetworking/plugins/releases/download/${CNI_VERSION}/cni-plugins-linux-${ARCH}-${CNI_VERSION}.tgz | sudo tar -xz -C /opt/cni/bin

# Make a config folder for CNI definitions
sudo mkdir -p /etc/cni/net.d

# Make an initial loopback configuration
sudo sh -c 'cat >/etc/cni/net.d/99-loopback.conf <<-EOF
{
    "cniVersion": "0.3.1",
    "type": "loopback"
}
EOF'
```

### Get containerd

You have three options - binaries for PC, binaries for armhf, or build from source.

* Install containerd `x86_64` only

```bash
export VER=1.6.8
curl -sSL https://github.com/containerd/containerd/releases/download/v$VER/containerd-$VER-linux-amd64.tar.gz > /tmp/containerd.tar.gz \
  && sudo tar -xvf /tmp/containerd.tar.gz -C /usr/local/bin/ --strip-components=1

containerd -version
```

* Or get my containerd binaries for Raspberry Pi (armhf)

    Building `containerd` on armhf is extremely slow, so I've provided binaries for you.

    ```bash
    curl -sSL https://github.com/alexellis/containerd-armhf/releases/download/v1.6.8/containerd.tgz | sudo tar -xvz --strip-components=2 -C /usr/local/bin/
    ```

* Or clone / build / install [containerd](https://github.com/containerd/containerd) from source:

    ```bash
    export GOPATH=$HOME/go/
    mkdir -p $GOPATH/src/github.com/containerd
    cd $GOPATH/src/github.com/containerd
    git clone https://github.com/containerd/containerd
    cd containerd
    git fetch origin --tags
    git checkout v1.6.8

    make
    sudo make install

    containerd --version
    ```

#### Ensure containerd is running

```bash
curl -sLS https://raw.githubusercontent.com/containerd/containerd/v1.6.8/containerd.service > /tmp/containerd.service

# Extend the timeouts for low-performance VMs
echo "[Manager]" | tee -a /tmp/containerd.service
echo "DefaultTimeoutStartSec=3m" | tee -a /tmp/containerd.service

sudo cp /tmp/containerd.service /lib/systemd/system/
sudo systemctl enable containerd

sudo systemctl daemon-reload
sudo systemctl restart containerd
```

Or run ad-hoc. This step can be useful for exploring why containerd might fail to start.

```bash
sudo containerd &
```

#### Enable forwarding

> This is required to allow containers in containerd to access the Internet via your computer's primary network interface.

```bash
sudo /sbin/sysctl -w net.ipv4.conf.all.forwarding=1
```

Make the setting permanent:

```bash
echo "net.ipv4.conf.all.forwarding=1" | sudo tee -a /etc/sysctl.conf
```

### Hacking (build from source)

#### Get build packages

```bash
sudo apt update \
  && sudo apt install -qy \
    runc \
    bridge-utils \
    make
```

You may find alternative package names for CentOS and other Linux distributions.

#### Install Go 1.13 (x86_64)

```bash
curl -SLf https://golang.org/dl/go1.16.linux-amd64.tar.gz > /tmp/go.tgz
sudo rm -rf /usr/local/go/
sudo mkdir -p /usr/local/go/
sudo tar -xvf /tmp/go.tgz -C /usr/local/go/ --strip-components=1

export GOPATH=$HOME/go/
export PATH=$PATH:/usr/local/go/bin/

go version
```

You should also add the following to `~/.bash_profile`:

```bash
echo "export GOPATH=\$HOME/go/" | tee -a $HOME/.bash_profile
echo "export PATH=\$PATH:/usr/local/go/bin/" | tee -a $HOME/.bash_profile
```

#### Or on Raspberry Pi (armhf)

```bash
curl -SLsf https://golang.org/dl/go1.16.linux-armv6l.tar.gz > go.tgz
sudo rm -rf /usr/local/go/
sudo mkdir -p /usr/local/go/
sudo tar -xvf go.tgz -C /usr/local/go/ --strip-components=1

export GOPATH=$HOME/go/
export PATH=$PATH:/usr/local/go/bin/

go version
```

#### Clone core and its systemd unit files

```bash
mkdir -p $GOPATH/src/github.com/faasedge/
cd $GOPATH/src/github.com/faasedge/
git clone https://github.gatech.edu/faasedge/core
```

#### Build `core` from source (optional)

```bash
cd $GOPATH/src/github.com/faasedge/core
cd core
make local

# Install the binary
sudo cp bin/core /usr/local/bin
```


#### Install `core`

This step installs core as a systemd unit file, creates files in `/var/lib/core`, and writes out networking configuration for the CNI bridge networking plugin.

```bash
sudo core install

2020/02/17 17:38:06 Writing to: "/var/lib/core/secrets/basic-auth-password"
2020/02/17 17:38:06 Writing to: "/var/lib/core/secrets/basic-auth-user"
Login with:
  sudo cat /var/lib/core/secrets/basic-auth-password | faas-cli login -s
```

You can now log in either from this machine or a remote machine using the OpenFaaS UI, or CLI.

Check that core is ready:

```bash
sudo journalctl -u core
```

You should see output like:

```bash
Feb 17 17:46:35 gold-survive core[4140]: 2020/02/17 17:46:35 Starting core proxy on 8080
Feb 17 17:46:35 gold-survive core[4140]: Gateway: 10.62.0.5:8080
Feb 17 17:46:35 gold-survive core[4140]: 2020/02/17 17:46:35 [proxy] Wait for done
Feb 17 17:46:35 gold-survive core[4140]: 2020/02/17 17:46:35 [proxy] Begin listen on 8080
```

To get the CLI for the command above run:

```bash
curl -sSLf https://cli.openfaas.com | sudo sh
```

#### Make a change to `core`

There are two components you can hack on:

For function CRUD you will work on `core provider` which is started from `cmd/provider.go`

For core itself, you will work on the code from `core up`, which is started from `cmd/up.go`

Before working on either, stop the systemd services:

```
sudo systemctl stop core &    # up command
sudo systemctl stop core-provider   # provider command
```

Here is a workflow you can use for each code change:

Enter the directory of the source code, and build a new binary:

```bash
cd $GOPATH/src/github.com/faasedge/core
go build
```

Copy that binary to `/usr/local/bin/`

```bash
cp core /usr/local/bin/
```

To run `core up`, run it from its working directory as root

```bash
sudo -i
cd /var/lib/core

core up
```

Now to run `core provider`, run it from its working directory:

```bash
sudo -i
cd /var/lib/core-provider

core provider
```

#### At run-time

Look in `hosts` in the current working folder or in `/var/lib/core/` to get the IP for the gateway or Prometheus

```bash
127.0.0.1      localhost
10.62.0.1      core-provider

10.62.0.2      prometheus
10.62.0.3      gateway
10.62.0.4      nats
10.62.0.5      queue-worker
```

The IP addresses are dynamic and may change on every launch.

Since core-provider uses containerd heavily it is not running as a container, but as a stand-alone process. Its port is available via the bridge interface, i.e. `openfaas0`

* Prometheus will run on the Prometheus IP plus port 8080 i.e. http://[prometheus_ip]:9090/targets

* core-provider runs on 10.62.0.1:8081, i.e. directly on the host, and accessible via the bridge interface from CNI.

* Now go to the gateway's IP address as shown above on port 8080, i.e. http://[gateway_ip]:8080 - you can also use this address to deploy OpenFaaS Functions via the `faas-cli`. 

* basic-auth

    You will then need to get the basic-auth password, it is written to `/var/lib/core/secrets/basic-auth-password` if you followed the above instructions.
The default Basic Auth username is `admin`, which is written to `/var/lib/core/secrets/basic-auth-user`, if you wish to use a non-standard user then create this file and add your username (no newlines or other characters) 

#### Installation with systemd

* `core install` - install core and containerd with systemd, this must be run from `$GOPATH/src/github.com/faasedge/core`
* `journalctl -u core -f` - core service logs
* `journalctl -u core-provider -f` - core-provider service logs

#### Uninstall

* Stop core and core-provider
```
sudo systemctl stop core
sudo systemctl stop core-provider
sudo systemctl stop containerd
```

* Remove core from machine
```
sudo systemctl disable core
sudo systemctl disable core-provider
sudo systemctl disable containerd
sudo rm -rf /usr/local/bin/core
sudo rm -rf /var/lib/core
sudo rm -rf /usr/lib/systemd/system/core-provider.service
sudo rm -rf /usr/lib/systemd/system/core.service
sudo rm -rf /usr/lib/systemd/system/containerd
sudo systemctl daemon-reload
```

* Remove additional dependencies. Be cautious as other software will be dependent on these.
```
sudo apt-get remove runc bridge-utils
sudo rm -rf /opt/cni/bin
```