#!/bin/bash

# Exit immediately if a command exits with a non-zero status
set -e

# Function to print messages
echo_message() {
    echo "==> $1"
}

# Install Go
install_go() {
    echo_message "Installing Go"
    GO_VERSION=1.18
    wget https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz
    sudo tar -C /usr/local -xzf go${GO_VERSION}.linux-amd64.tar.gz
    echo "export PATH=\$PATH:/usr/local/go/bin" | sudo tee /etc/profile.d/go.sh
    sudo chmod +x /etc/profile.d/go.sh
    source /etc/profile.d/go.sh
    rm go${GO_VERSION}.linux-amd64.tar.gz
    echo_message "Go installed: $(go version)"
}

# Install Arkade
install_arkade() {
    echo_message "Installing Arkade"
    curl -sLS https://get.arkade.dev | sudo sh
}

# Install required packages
install_required_packages() {
    echo_message "Installing required packages"
    sudo apt-get update -y
    sudo apt-get install -y curl runc bridge-utils
}

# Install CNI plugins
install_cni_plugins() {
    echo_message "Installing CNI plugins"
    CNI_VERSION=v0.9.1
    sudo arkade system install cni --version ${CNI_VERSION} --path /opt/cni/bin --progress=false
}

# Install containerd
install_containerd() {
    echo_message "Installing containerd"
    CONTAINERD_VER=1.6.8
    sudo systemctl unmask containerd || :
    sudo arkade system install containerd --systemd --version v${CONTAINERD_VER} --progress=false
    sudo systemctl enable containerd
    sudo systemctl start containerd
}

# Clone fecore repository
clone_fecore_repo() {
    echo_message "Cloning fecore repository"
    sudo mkdir -p /usr/local/src
    sudo chown $(whoami):faasedge-PG0 /usr/local/src
    cd /usr/local/src
    git clone https://github.gatech.edu/dcao48/fecore.git
    cd fecore
    go mod tidy
    go build
    sudo cp fecore /usr/local/bin/ -f
}

# Install fecore
install_fecore() {
    echo_message "Installing fecore"
    cd /usr/local/src/fecore
    # Create necessary directories
    sudo mkdir -p /var/lib/fecore/secrets
    sudo mkdir -p /var/lib/fecore-provider

    # Copy configuration files
    sudo cp docker-compose.yaml /var/lib/fecore/
    sudo cp prometheus.yml /var/lib/fecore/
    sudo cp resolv.conf /var/lib/fecore/

    # Ensure fecore binary is in /usr/local/bin
    if [ ! -f /usr/local/bin/fecore ]; then
        echo "fecore binary not found in /usr/local/bin"
        exit 1
    fi
}

# Enable IP forwarding
enable_ip_forwarding() {
    echo_message "Enabling IP forwarding"
    sudo sysctl -w net.ipv4.conf.all.forwarding=1
    echo "net.ipv4.conf.all.forwarding=1" | sudo tee -a /etc/sysctl.conf
}

# Install faas-cli
install_faas_cli() {
    echo_message "Installing faas-cli"
    sudo arkade get faas-cli
    sudo install -m 755 $HOME/.arkade/bin/faas-cli /usr/local/bin/
}

# Main script execution
main() {
    install_go
    install_arkade
    install_required_packages
    install_cni_plugins
    install_containerd
    clone_fecore_repo
    install_fecore
    enable_ip_forwarding
    install_faas_cli

    echo_message "Installation completed successfully!"
}

# Execute main function
main

