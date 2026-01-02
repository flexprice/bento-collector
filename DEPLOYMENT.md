# Deployment Guide

This guide explains how to build and deploy the Bento Collector Docker image to AWS ECR for use with ECS.

## Prerequisites

1. **AWS Account** with ECR repository created
2. **AWS CLI** installed and configured
3. **Docker** installed and running
4. **Git** for commit hash tagging

## ECR Repository Setup

First, create an ECR repository in AWS:

```bash
aws ecr create-repository \
    --repository-name bento-collector \
    --region us-east-1 \
    --image-scanning-configuration scanOnPush=true \
    --encryption-configuration encryptionType=AES256
```

## Configuration

### 1. Environment Variables

Add the following to your `.env` file (copy from `env.example`):

```bash
ECR_REGISTRY=123456789012.dkr.ecr.us-east-1.amazonaws.com
ECR_REPOSITORY=bento-collector
AWS_REGION=us-east-1
```

Replace `123456789012` with your AWS account ID.

### 2. GitHub Actions Setup (for CI/CD)

For automated builds via GitHub Actions, configure the following:

#### Repository Variables

Go to your GitHub repository → Settings → Secrets and variables → Actions → Variables, and add:

- `ECR_REGISTRY`: Your ECR registry URL (e.g., `123456789012.dkr.ecr.us-east-1.amazonaws.com`)
- `ECR_REPOSITORY`: Your ECR repository name (e.g., `bento-collector`)
- `AWS_REGION`: AWS region (e.g., `us-east-1`)

#### Repository Secrets

Add the following secret:

- `AWS_ROLE_ARN`: ARN of the IAM role for GitHub Actions OIDC authentication

Example: `arn:aws:iam::123456789012:role/github-actions-ecr-role`

#### IAM Role Setup for GitHub Actions

Create an IAM role with the following trust policy:

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Principal": {
        "Federated": "arn:aws:iam::123456789012:oidc-provider/token.actions.githubusercontent.com"
      },
      "Action": "sts:AssumeRoleWithWebIdentity",
      "Condition": {
        "StringEquals": {
          "token.actions.githubusercontent.com:aud": "sts.amazonaws.com"
        },
        "StringLike": {
          "token.actions.githubusercontent.com:sub": "repo:YOUR_ORG/YOUR_REPO:*"
        }
      }
    }
  ]
}
```

Attach the following policy to the role:

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": [
        "ecr:GetAuthorizationToken",
        "ecr:BatchCheckLayerAvailability",
        "ecr:GetDownloadUrlForLayer",
        "ecr:BatchGetImage",
        "ecr:PutImage",
        "ecr:InitiateLayerUpload",
        "ecr:UploadLayerPart",
        "ecr:CompleteLayerUpload"
      ],
      "Resource": "*"
    }
  ]
}
```

## Manual Build and Push

### Using the Script

The easiest way to build and push manually:

```bash
./scripts/build_and_push_ecr.sh [tag]
```

Examples:

```bash
# Use commit hash as tag
./scripts/build_and_push_ecr.sh

# Use custom tag
./scripts/build_and_push_ecr.sh v1.0.0

# Use branch name
./scripts/build_and_push_ecr.sh develop
```

### Manual Docker Commands

If you prefer to run commands manually:

```bash
# 1. Login to ECR
aws ecr get-login-password --region us-east-1 | \
    docker login --username AWS --password-stdin \
    YOUR_ACCOUNT_ID.dkr.ecr.us-east-1.amazonaws.com

# 2. Build the image
docker buildx build \
    --platform linux/arm64 \
    --load \
    -t YOUR_ACCOUNT_ID.dkr.ecr.us-east-1.amazonaws.com/bento-collector:latest \
    -f Dockerfile.ecs .

# 3. Push the image
docker push YOUR_ACCOUNT_ID.dkr.ecr.us-east-1.amazonaws.com/bento-collector:latest
```

## GitHub Actions Workflow

The workflow (`.github/workflows/deploy.yml`) automatically:

- Builds and pushes on pushes to `main` or `develop` branches
- Tags images appropriately:
  - `main` branch → `latest` tag
  - `develop` branch → `develop` tag
  - Tags (v*) → version tag
  - All commits → commit hash tag
- Uses OIDC for secure authentication (no long-lived credentials)
- Caches Docker layers for faster builds

### Triggering Manually

You can trigger the workflow manually from GitHub Actions tab:

1. Go to Actions → "Build and Push to ECR"
2. Click "Run workflow"
3. Optionally provide a custom tag

## Using the Image in ECS

### Task Definition

Create an ECS task definition that uses the ECR image. The image supports command overrides to run different collectors.

#### Example Task Definition (JSON)

```json
{
  "family": "bento-collector",
  "networkMode": "awsvpc",
  "requiresCompatibilities": ["FARGATE"],
  "cpu": "256",
  "memory": "512",
  "containerDefinitions": [
    {
      "name": "bento-collector",
      "image": "YOUR_ACCOUNT_ID.dkr.ecr.us-east-1.amazonaws.com/bento-collector:latest",
      "essential": true,
      "command": [
        "-c",
        "/app/examples/kafka/consume-from-kafka.yaml"
      ],
      "environment": [
        {
          "name": "FLEXPRICE_API_HOST",
          "value": "api.cloud.flexprice.io"
        },
        {
          "name": "FLEXPRICE_API_KEY",
          "value": "your_api_key"
        }
      ],
      "logConfiguration": {
        "logDriver": "awslogs",
        "options": {
          "awslogs-group": "/ecs/bento-collector",
          "awslogs-region": "us-east-1",
          "awslogs-stream-prefix": "ecs"
        }
      }
    }
  ]
}
```

#### Using AWS CLI

```bash
aws ecs register-task-definition \
    --cli-input-json file://task-definition.json
```

### Running Different Collectors

You can run different collectors by overriding the command in the task definition:

```bash
# Kafka consumer
-c /app/examples/kafka/consume-from-kafka.yaml

# Kafka consumer with DLQ
-c /app/examples/kafka/consume-from-kafka-with-dlq.yaml

# Dummy events generator
-c /app/examples/dummy-events-to-flexprice.yaml
```

### Custom Configuration Files

To use your own YAML configuration:

1. **Option 1**: Mount a volume with your config file
2. **Option 2**: Build a custom image with your configs included
3. **Option 3**: Use ECS task definition to override the command with a config from S3 or mounted volume

Example with S3 (requires additional setup):

```json
{
  "command": [
    "-c",
    "/app/config/my-custom-collector.yaml"
  ],
  "mountPoints": [
    {
      "sourceVolume": "config",
      "containerPath": "/app/config"
    }
  ]
}
```

## Image Tags

The workflow and script create multiple tags:

- **Branch-based**: `latest` (main), `develop` (develop branch)
- **Version tags**: `v1.0.0`, `v1.2.3`, etc. (from git tags)
- **Commit hash**: Short commit hash (e.g., `a1b2c3d`)

This allows you to:
- Use `latest` for production
- Use `develop` for staging
- Pin to specific versions or commits for reproducibility

## Security Best Practices

1. **OIDC Authentication**: GitHub Actions uses OIDC (no long-lived credentials)
2. **Non-root user**: Container runs as non-root user (`bento`)
3. **Minimal base image**: Uses Alpine Linux for smaller attack surface
4. **Image scanning**: Enable ECR image scanning (configured in repository setup)
5. **Secrets management**: Use AWS Secrets Manager or ECS task secrets for sensitive data

## Troubleshooting

### Build fails with "unauthorized"

- Check AWS credentials: `aws sts get-caller-identity`
- Verify ECR repository exists and you have permissions
- Ensure you're logged in: `aws ecr get-login-password --region us-east-1 | docker login ...`

### Push fails with "repository not found"

- Verify `ECR_REGISTRY` and `ECR_REPOSITORY` are correct
- Ensure repository exists: `aws ecr describe-repositories`

### GitHub Actions fails

- Check repository variables are set correctly
- Verify IAM role ARN in secrets
- Check OIDC provider is configured in AWS
- Review workflow logs for detailed error messages

### ECS task fails to start

- Verify image URI is correct
- Check task definition command override
- Review CloudWatch logs for container errors
- Ensure environment variables are set correctly

## Additional Resources

- [AWS ECR Documentation](https://docs.aws.amazon.com/ecr/)
- [ECS Task Definitions](https://docs.aws.amazon.com/AmazonECS/latest/developerguide/task_definitions.html)
- [GitHub Actions OIDC](https://docs.github.com/en/actions/deployment/security-hardening-your-deployments/configuring-openid-connect-in-amazon-web-services)


