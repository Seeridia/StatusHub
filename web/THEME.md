# StatusHub theme

The frontend uses `@okramp/core` and `@okramp/tdesign`, following the
[OKRamp guide](https://okramp.seeridia.top/?page=guide&guideDoc=getting-started/overview).

Edit `brandSeed` in `scripts/generate-theme.mjs` to change the brand hue. It
currently uses the logo's mint green, `#20C897`. Run `npm run theme:generate`
to regenerate `src/theme.css`; `npm run dev` and `npm run build` also do this
automatically. Commit the generated CSS with the generator changes.

The `radii` configuration in the same script sets the global TDesign radius
scale: small 4px, default 8px, medium 12px, large 16px, extraLarge 20px.
Both appearances share this scale; round and circle tokens retain their
original shapes. Components and custom surfaces inherit these tokens.

The generated stylesheet loads after TDesign's base styles and defines both
appearances on the document root, including overlays mounted under `body`.
The existing Light / Dark / System setting switches `theme-mode` without
regenerating the palette. Login retains its existing light appearance.
Application styles consume semantic `--td-*` variables. Status colors keep
their TDesign meanings. Static logo and illustration assets are unchanged.

`contrastPolicy: "adjust"` selects readable tones from the seed's palette;
the primary button is therefore darker than the original logo in light mode.
Generation fails if any engine-reported contrast target is unmet. These
checks cover engine semantics, not every possible component composition.
Package versions are pinned to keep palette generation reproducible.

## Desktop interface refinements

- List toolbars now display their page title and description, with one shared
  heading treatment and consistent action placement.
- Channel, notification-rule and source forms use shared controlled validation
  with TDesign FormItem `status`, `tips` and required markers. Invalid submission
  focuses the first invalid control; correcting values updates feedback, and
  reopening an editor resets validation. Server errors remain form-level alerts.
- Form labels target their controls, with field-level `aria-invalid`,
  `aria-required` and error-message associations.
- Removed unused legacy login/landing CSS and superseded declarations in
  identical selector scopes. The current sign-in illustration and the user's
  unrestricted workspace width are retained.
- DS-001, DS-002, DS-003: shared TDesign components and semantic tokens retained.
  DS-004, DS-005: desktop shell retained. DS-006, DS-007: existing destructive
  confirmations and query states retained. DS-008 through DS-011: existing
  icons, contrast, tables and status labels retained. DS-012: review recorded.

## Theme verification

- Production build, TypeScript and translation coverage passed.
- All 14 existing frontend tests passed.
- Browser checks: light overview, dark rule editor, theme switching and a
  body-mounted vendor dropdown render with the generated theme.
- DS-001, DS-002, DS-009, DS-011: passed for this theme change; existing
  components use shared semantic tokens and retain status text/icons.
- DS-003: radius customization is centralized in theme tokens; application
  styles continue to reference semantic radius variables.
- DS-004, DS-005, DS-006, DS-007, DS-008, DS-010: not applicable to
  this change; layout, navigation, confirmations, data states,
  icons and table implementations were not modified.
- DS-012: review recorded here. The static quality script does not pass for
  the existing frontend: it also flags pre-existing layout/style patterns
  and generated palette literals. Palette definitions necessarily contain
  literal colors; application styles reference their semantic variables.
