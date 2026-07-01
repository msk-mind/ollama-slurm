# Model Config

This directory is for normalized broker-readable model profiles.

Profiles in this area should describe broker-facing capabilities such as:

- model identifier and runtime
- context and token limits
- accelerator requirements
- backend compatibility
- task suitability
- scheduling hints such as preferred QoS or execution class

Current repository state:

- `model_configs/` remains the current operational source for direct launch scripts
- this directory is the target home for broker-readable profiles that are decoupled from shell submission logic
