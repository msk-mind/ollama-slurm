# Local Backend Assets

Local execution assets live here.

Current contents:

- `broker_worker.sh`: supported direct-execution worker entrypoint for laptops, workstations, and single hosts

This directory is for backend assets that do not require a scheduler. The local backend is intended for:

- development on the current machine
- small-scale interactive validation
- workstation execution on Linux or macOS

The local backend reuses the same staged broker job bundle and worker contract as the Slurm backend. It differs only in how the worker process is launched and tracked.
