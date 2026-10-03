QORA-10 visual evidence, captured with Playwright after authorization on 2026-10-03 UTC.

These are local UI Lab component captures with fixed test data, not production screenshots. The composer, editor, menus, task popover, issue chips, avatars, hooks, and stores are the real product modules. API data and navigation are fixtures. No agent execution or production data access was involved.

- `before.png`: `ChatInput`, `ChatPlanMode`, and `ChatCards` from baseline `2d7d0d6b2`, with the original Chinese plan label, rendered in the same fixture host.
- `after.png`: composer from `46875ed0b`, plan and chat parameters enabled, task list collapsed.
- `tasks.png`: expanded related-task popover with status, identifier, truncated title, and assignee avatar.
- `menu.png`: plan and execution settings in the add menu, including inherited, Standard, and Fast service tiers.
- `narrow.png`: 360px viewport; the three tags remain on one row and the page does not overflow horizontally.
- `dark.png`: dark appearance of the updated composer.

Browser assertions passed for the collapsed/default task list, three navigable task rows, hidden entry with no tasks, plan toggle and removal, high/Fast selection and clearing, and the narrow layout. No uncaught page errors occurred. This verifies component UI behavior; backend and daemon execution are covered by the separately reported Go tests.
