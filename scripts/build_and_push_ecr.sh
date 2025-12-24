#!/bin/bash
set -euo pipefail

# Script to build and push Docker image to AWS ECR
# Usage: ./scripts/build_and_push_ecr.sh [tag]
# If tag is not provided, uses commit hash

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Function to print colored output
print_info() {
    echo -e "${GREEN}[INFO]${NC} $1"
}

print_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

print_warn() {
    echo -e "${YELLOW}[WARN]${NC} $1"
}

# Load environment variables from .env file
if [ -f .env ]; then
    print_info "Loading environment variables from .env file..."
    # More robust way to load env vars, handling comments properly
    while IFS= read -r line || [[ -n "$line" ]]; do
        # Skip empty lines and comments
        [[ -z "$line" || "$line" =~ ^[[:space:]]*# ]] && continue
        # Export valid environment variables
        if [[ "$line" =~ ^[[:alpha:]][[:alnum:]_]*= ]]; then
            # Split key and value
            key="${line%%=*}"
            value="${line#*=}"
            # Strip surrounding quotes (both single and double) from value
            value="${value#\"}"
            value="${value%\"}"
            value="${value#\'}"
            value="${value%\'}"
            # Export the cleaned variable
            export "${key}=${value}"
        fi
    done < .env
else
    print_warn ".env file not found. Using environment variables from shell."
fi

# Check if required variables exist
if [[ -z "${ECR_REGISTRY:-}" ]]; then
    print_error "ECR_REGISTRY not found. Please set it in .env file or environment."
    print_info "Example: ECR_REGISTRY=123456789012.dkr.ecr.us-east-1.amazonaws.com"
    exit 1
fi

if [[ -z "${ECR_REPOSITORY:-}" ]]; then
    print_error "ECR_REPOSITORY not found. Please set it in .env file or environment."
    print_info "Example: ECR_REPOSITORY=bento-collector"
    exit 1
fi

# Set AWS region (default to us-east-1)
AWS_REGION="${AWS_REGION:-us-east-1}"

# Check if AWS CLI is installed
if ! command -v aws &> /dev/null; then
    print_error "AWS CLI is not installed. Please install it first."
    print_info "Visit: https://aws.amazon.com/cli/"
    exit 1
fi

# Check if Docker is installed
if ! command -v docker &> /dev/null; then
    print_error "Docker is not installed. Please install it first."
    exit 1
fi

# Login to Amazon ECR
print_info "Logging in to Amazon ECR (region: ${AWS_REGION})..."
if ! aws ecr get-login-password --region "${AWS_REGION}" | docker login --username AWS --password-stdin "${ECR_REGISTRY}"; then
    print_error "Failed to login to ECR. Please check your AWS credentials."
    exit 1
fi

# Get commit hash (short)
COMMIT_HASH=$(git rev-parse --short HEAD 2>/dev/null || echo "local")

# Determine tag
if [ $# -ge 1 ]; then
    TAG="$1"
    print_info "Using provided tag: ${TAG}"
else
    TAG="${COMMIT_HASH}"
    print_info "Using commit hash as tag: ${TAG}"
fi

# Build full image URI
IMAGE_URI="${ECR_REGISTRY}/${ECR_REPOSITORY}:${TAG}"
COMMIT_IMAGE_URI="${ECR_REGISTRY}/${ECR_REPOSITORY}:${COMMIT_HASH}"

print_info "Building image: ${IMAGE_URI}"

# Check if Docker daemon is accessible
if ! docker info &> /dev/null; then
    print_error "Cannot connect to Docker daemon. Please ensure Docker/OrbStack is running."
    exit 1
fi

# Detect OrbStack and set DOCKER_HOST if needed
if [[ -z "${DOCKER_HOST:-}" ]]; then
    if [[ -S /var/run/docker.sock ]]; then
        export DOCKER_HOST=unix:///var/run/docker.sock
        print_info "Detected OrbStack, using /var/run/docker.sock"
    elif [[ -S ~/.orbstack/run/docker.sock ]]; then
        export DOCKER_HOST=unix://${HOME}/.orbstack/run/docker.sock
        print_info "Detected OrbStack, using ~/.orbstack/run/docker.sock"
    fi
fi

# Initialize buildx if needed
if ! docker buildx version &> /dev/null; then
    print_error "Docker buildx is not available. Please update Docker/OrbStack."
    exit 1
fi

# Remove existing custom builder if it exists (might be misconfigured)
if docker buildx inspect builder &> /dev/null; then
    print_info "Removing existing buildx builder to ensure correct configuration..."
    docker buildx rm builder 2>/dev/null || true
fi

# Use default builder (works best with OrbStack/Docker Desktop)
print_info "Setting up Docker buildx builder..."
if docker buildx inspect default &> /dev/null; then
    docker buildx use default
else
    # If default doesn't exist, create it
    print_info "Creating default buildx builder..."
    docker buildx create --name default --use --driver docker-container || {
        print_error "Failed to create buildx builder. Please ensure Docker/OrbStack is running."
        print_info "You can try: docker buildx create --name default --use"
        exit 1
    }
fi

# Build the image
print_info "Building Docker image for platform linux/arm64..."
if ! docker buildx build \
    --platform linux/arm64 \
    --load \
    -t "${IMAGE_URI}" \
    -f Dockerfile.ecs \
    .; then
    print_error "Docker build failed."
    exit 1
fi

# Also tag with commit hash if different from main tag
if [ "${TAG}" != "${COMMIT_HASH}" ]; then
    print_info "Tagging image with commit hash: ${COMMIT_IMAGE_URI}"
    docker tag "${IMAGE_URI}" "${COMMIT_IMAGE_URI}"
fi

# Push the image(s)
print_info "Pushing image to ECR: ${IMAGE_URI}"
if ! docker push "${IMAGE_URI}"; then
    print_error "Failed to push image to ECR."
    exit 1
fi

# Push commit hash tag if different
if [ "${TAG}" != "${COMMIT_HASH}" ]; then
    print_info "Pushing commit hash tag: ${COMMIT_IMAGE_URI}"
    docker push "${COMMIT_IMAGE_URI}"
fi

print_info "✅ Image successfully built and pushed to ECR!"
echo ""
echo "Image URI: ${IMAGE_URI}"
if [ "${TAG}" != "${COMMIT_HASH}" ]; then
    echo "Commit hash tag: ${COMMIT_IMAGE_URI}"
fi
echo ""
print_info "You can now use this image in your ECS task definition."
print_info "Example command override: -c /app/examples/dummy-events-to-flexprice.yaml"

