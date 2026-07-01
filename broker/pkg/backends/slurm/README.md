# Slurm Backend

This package should implement the backend adapter for Slurm.

Initial responsibilities:

- submit broker jobs as ordinary Slurm jobs
- map Slurm job states into broker states
- cancel jobs
- surface logs and terminal metadata for reconciliation

