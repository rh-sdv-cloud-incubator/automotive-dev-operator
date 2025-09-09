# Automotive Dev Operator Web UI

A React-based web interface for creating and managing automotive OS image builds using PatternFly 6.

## Features

- **Create Builds**: Interactive form with free text inputs for all build parameters
- **External Files**: Create text files and upload binary files for builds
- **Monitor Builds**: Real-time build status and progress tracking
- **View Logs**: Stream build logs in real-time
- **Download Artifacts**: Download completed build artifacts
- **Responsive Design**: Modern UI using PatternFly 6 components

## Development

### Prerequisites

- Node.js 18+ 
- npm 9+

### Getting Started

1. Install dependencies:
```bash
npm ci
```

2. Start the development server:
```bash
npm start
```

The application will start on http://localhost:3000 and proxy API calls to http://localhost:8080.

### Building for Production

```bash
npm run build
```

## Docker

### Build the Docker image:

```bash
docker build -t automotive-dev-webui .
```

### Run the container:

```bash
docker run -p 8080:80 automotive-dev-webui
```

## Configuration

The application expects the REST API to be available at `/v1/` endpoints. In production, nginx proxies these requests to the `automotive-dev-build-api` service.

## API Integration

The web UI integrates with the following REST API endpoints:

- `POST /v1/builds` - Create a new build
- `GET /v1/builds` - List all builds  
- `GET /v1/builds/{name}` - Get build details
- `GET /v1/builds/{name}/logs` - Stream build logs
- `GET /v1/builds/{name}/artifact/{filename}` - Download build artifact by filename

## Form Fields

All form fields support free text input as requested:

- **Build Name**: Unique identifier for the build
- **Manifest Content**: YAML content for the build manifest
- **Distribution**: Target distribution (default: cs9)
- **Target**: Build target (default: qemu)  
- **Architecture**: Target architecture (default: arm64)
- **Export Format**: Output format (default: image)
- **Mode**: Build mode (default: image)
- **Container Image**: automotive-image-builder container to use
- **AIB Extra Args**: Additional arguments for automotive-image-builder
- **AIB Override Args**: Complete override of AIB arguments
- **Enable Artifact Downloads**: Allow downloading build artifacts (recommended)