# Workers

Task-specific worker implementations live here.

Each worker should:

- accept an explicit execution bundle
- process only staged inputs
- emit schema-validated JSON results
- emit typed artifacts and metadata

Initial workers:

- `document-summary/`
- `log-analysis/`
- `repo-summary/`

