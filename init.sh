#!/bin/bash
set -e

echo "=== FaaSEdge Core Setup Script ==="
echo "Setting up environment for FaaSEdge Core deployment"

# Check GRUB settings
REQUIRED_GRUB_SETTINGS="processor.max_cstate=0 idle=poll intel_idle.max_cstate=0 nosmt=force"
GRUB_FILE="/etc/default/grub"
GRUB_SETTINGS_MISSING=false

echo "Checking GRUB settings..."
if [ -f "$GRUB_FILE" ]; then
    # Check if any GRUB_CMDLINE_LINUX line contains all required settings
    if ! grep -q "GRUB_CMDLINE_LINUX=.*processor.max_cstate=0.*idle=poll.*intel_idle.max_cstate=0.*nosmt=force" "$GRUB_FILE"; then
        GRUB_SETTINGS_MISSING=true
    fi
else
    GRUB_SETTINGS_MISSING=true
fi

# Install base dependencies
apt-get update
apt-get install -y \
  git \
  curl \
  wget \
  build-essential \
  python3-pip \
  net-tools \
  sudo \
  libgl1-mesa-glx \
  libglib2.0-0 \
  libc6-dev \
  msr-tools

pip3 install psutil numpy opencv-python requests posix_ipc

# Install Redis using apt
echo "Installing Redis..."
apt-get install -y redis

# Try to stop Redis service, but handle the case where it's not running
if systemctl is-active --quiet redis; then
    echo "Stopping Redis service..."
    systemctl stop redis
    systemctl disable redis
    echo "Redis service stopped and disabled"
else
    echo "Redis service is not running (good!)"
fi

# Double-check no Redis is running via process check
if pgrep redis-server > /dev/null; then
    echo "Found Redis process, stopping it..."
    pkill redis-server
    echo "Redis process stopped"
else
    echo "No Redis process found (good!)"
fi

echo "Redis setup completed - ready for FaaSEdge Core to manage Redis"

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

echo "=== !!Important Settings!! Make sure to apply grub settings and rebooted before running this script==="
GRUB_CMDLINE_LINUX="console=ttyS0,115200 emulabcnet=14:58:d0:58:1f:a2 processor.max_cstate=0 idle=poll intel_idle.max_cstate=0 nosmt=force"

# CPU Throttling
echo "=== CPU Frequency Control Setup ==="
echo "Setting up CPU frequency control for consistent performance..."

echo "1. Setting CPU governor to userspace for manual frequency control..."
sudo sh -c 'for i in /sys/devices/system/cpu/cpu*/cpufreq/scaling_governor; do echo userspace > $i; done'

echo "2. Setting CPU frequency to 2.0GHz (2000000 kHz)..."
sudo sh -c 'for i in /sys/devices/system/cpu/cpu*/cpufreq/scaling_min_freq; do echo 2000000 > $i; done'
sudo sh -c 'for i in /sys/devices/system/cpu/cpu*/cpufreq/scaling_max_freq; do echo 2000000 > $i; done'
sudo sh -c 'for i in /sys/devices/system/cpu/cpu*/cpufreq/scaling_setspeed; do echo 2000000 > $i; done'

# If needed or exists we can add this (Not needed for current setup):
# Disable turbo boost (acpi-cpufreq fallback)
# if [ -f /sys/devices/system/cpu/cpufreq/boost ]; then
#     echo 0 | sudo tee /sys/devices/system/cpu/cpufreq/boost
# fi
# Disable ondemand service if present
# sudo systemctl disable ondemand

echo "3. Disabling CPU turbo boost..."
# Check if intel_pstate is available
if [ -f /sys/devices/system/cpu/intel_pstate/no_turbo ]; then
    if ! echo 1 | sudo tee /sys/devices/system/cpu/intel_pstate/no_turbo > /dev/null; then
        echo "Warning: Could not disable turbo boost through intel_pstate. Checking current state..."
        cat /sys/devices/system/cpu/intel_pstate/no_turbo
    fi
else
    echo "Warning: intel_pstate interface not found. Turbo boost control might not be available."
fi

# Always try MSR method as backup
echo "4. Setting MSR register to disable turbo..."
sudo modprobe msr
if ! sudo wrmsr -a 0x1a0 0x4000850089; then
    echo "Warning: Could not set MSR register. Turbo boost might still be enabled."
fi

echo "5. Verifying CPU settings..."
echo "Current CPU governor:"
cat /sys/devices/system/cpu/cpu*/cpufreq/scaling_governor
echo "Current CPU driver:"
cat /sys/devices/system/cpu/cpu0/cpufreq/scaling_driver
echo "Current CPU frequencies:"
cat /proc/cpuinfo | grep MHz
echo "CPU frequency limits:"
cat /sys/devices/system/cpu/cpu*/cpufreq/scaling_min_freq
cat /sys/devices/system/cpu/cpu*/cpufreq/scaling_max_freq

echo "=== CPU Frequency Control Setup Complete ==="
echo

# Install faas-cli
echo "Installing faas-cli..."
curl -fSL https://cli.openfaas.com | sh


# Login to OpenFaaS
echo "Setting up OpenFaaS login..."
cat > /usr/local/bin/openfaas-login.sh << EOL
#!/bin/bash
sudo cat /var/lib/fecore/secrets/basic-auth-password | sudo faas-cli login -g http://127.0.0.1:8081 --password-stdin
export OPENFAAS_URL=http://127.0.0.1:8081
EOL
chmod +x /usr/local/bin/openfaas-login.sh

if [ "$GRUB_SETTINGS_MISSING" = true ]; then
    echo
    echo "=== !!WARNING: Required GRUB Settings Missing!! ==="
    echo "Please ensure $GRUB_FILE contains a line with:"
    echo "GRUB_CMDLINE_LINUX=\"... processor.max_cstate=0 idle=poll intel_idle.max_cstate=0 nosmt=force ...\""
    echo
    echo "Then run:"
    echo "sudo update-grub"
    echo "sudo reboot"
    echo
    echo "After reboot, run this script again."
    echo "================================================"
    exit 1
fi

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