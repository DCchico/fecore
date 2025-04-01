#!/bin/bash
set -e

echo "=== FaaSEdge Core Setup Script ==="
echo "Setting up environment for FaaSEdge Core deployment"

# Install base dependencies
apt-get update
apt-get install -y \
  git \
  curl \
  wget \
  build-essential \
  net-tools \
  sudo

# Install Redis using apt
echo "Installing Redis..."
apt-get install -y redis
systemctl stop redis
echo "Redis installed and stopped (fecore will manage it)"

# Install Docker
echo "Installing Docker..."
curl -fsSL https://get.docker.com -o get-docker.sh
sh get-docker.sh
systemctl enable docker
systemctl start docker

# Install containerd
echo "Installing containerd..."
# Using instructions from faasd docs
export VER=1.6.8
curl -fSL https://github.com/containerd/containerd/releases/download/v${VER}/containerd-${VER}-linux-amd64.tar.gz > /tmp/containerd.tar.gz
mkdir -p /usr/local/bin/
tar xvf /tmp/containerd.tar.gz -C /usr/local

# Install containerd service
curl -fSL https://raw.githubusercontent.com/containerd/containerd/main/containerd.service > /etc/systemd/system/containerd.service

# Start containerd
systemctl daemon-reload
systemctl enable containerd
systemctl start containerd

# Install faas-cli
echo "Installing faas-cli..."
curl -fSL https://cli.openfaas.com | sh


# Build and install fecore
echo "Building FaaSEdge Core..."
cd /usr/local/src/fecore
go build
cp fecore /usr/local/bin/ -f


# Login to OpenFaaS
echo "Setting up OpenFaaS login..."
cat > /usr/local/bin/openfaas-login.sh << EOL
#!/bin/bash
cat /var/lib/fecore/secrets/basic-auth-password | faas-cli login -g http://127.0.0.1:8080 --password-stdin
EOL
chmod +x /usr/local/bin/openfaas-login.sh

echo "=== Setup Complete ==="
echo "To start FaaSEdge Core:"
echo "1. In terminal 1: sudo fecore up"
echo "2. In terminal 2: sudo secret_mount_path=/var/lib/fecore/secrets fecore provider"
echo
echo "To login to OpenFaaS:"
echo "sudo openfaas-login.sh"
echo
echo "Known issue: Function resource limits aren't being passed correctly to the deployment system."
echo "Check deploy.go and the function registration code." 