#!/bin/bash

# AGFS Shell WebApp Setup Script

set -e

echo "🚀 Setting up AGFS Shell WebApp..."

# Check if uv is installed
if ! command -v uv &> /dev/null; then
    echo "❌ Error: uv is not installed"
    echo "Please install uv first: https://github.com/astral-sh/uv"
    exit 1
fi

# Check if npm is installed
if ! command -v npm &> /dev/null; then
    echo "❌ Error: npm is not installed"
    echo "Please install Node.js and npm first"
    exit 1
fi

# Install Python dependencies
echo "📦 Installing Python dependencies..."
cd "$(dirname "$0")/.."
uv sync --extra webapp

# Install frontend dependencies
echo "📦 Installing frontend dependencies..."
cd webapp
npm ci

# Build frontend
echo "🔨 Building frontend..."
npm run build

echo "✅ Setup complete!"
echo ""
echo "To start the web app, run:"
echo "  agfs-shell --webapp"
echo ""
echo "Or with custom host/port:"
echo "  agfs-shell --webapp --webapp-host 0.0.0.0 --webapp-port 8000"
