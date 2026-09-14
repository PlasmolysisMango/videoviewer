#!/bin/bash
set -e

echo "Building log receiver..."
cd "$(dirname "$0")"
go build -o log-receiver main.go
echo "Build complete: ./log-receiver"
echo ""
echo "To run manually:"
echo "  ./log-receiver"
echo ""
echo "To install as systemd service:"
echo "  sudo cp log-receiver.service /etc/systemd/system/"
echo "  sudo systemctl daemon-reload"
echo "  sudo systemctl enable log-receiver"
echo "  sudo systemctl start log-receiver"
