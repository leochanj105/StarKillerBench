# Prompt templates

Reusable agent prompts. Markdown with `{{KEY}}` placeholders.

- **Naming**: `<step-id>.md.tmpl`. The runner picks the template whose name matches the step's `id`.
- **Variables**: substituted from the `vars:` block in `steps.yaml`.
- **Style**: short. Each template states what the agent should read first, what to produce, and when to stop and ask. Templates link to canonical project docs rather than restating them.

Concrete templates are added as the project needs them.
