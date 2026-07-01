# Configs

Broker, model, and policy configuration belongs here.

Current configuration areas:

- `broker/`
- `models/`
- `policies/`

Current repository state:

- these directories define the target configuration layout for the broker project
- the legacy `model_configs/` directory remains the active source of model launch profiles for direct llama.cpp workflows

Configuration principles:

- keep deployable configuration out of application code where possible
- prefer normalized broker-readable formats over shell-specific config fragments
- separate broker behavior, model capability metadata, and policy rules into distinct bundles
