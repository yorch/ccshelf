---
name: component-review
description: Review a React component against the Acme design system: tokens, spacing, states and naming. Use when a pull request adds or changes a UI component.
---

# Component review

1. Read the component and its stories or tests.
2. Check that colors, spacing and typography use design tokens, not literal values.
3. List the states the component must handle (default, hover, focus, disabled, loading, error) and mark any that are missing.
4. Check naming against the system: `PascalCase` components, `data-` attributes for test hooks.
5. Report findings as a short checklist ordered by severity, with the file and line for each.
