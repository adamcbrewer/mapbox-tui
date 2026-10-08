# Go style

Use [samber's Go code-style skill](https://www.skills.sh/samber/cc-skills-golang/golang-code-style)
for clarity decisions and `gofmt` for formatting. Apply changes to the code being
worked on; preserve observable behaviour and CLI/MCP contracts.

## Project exceptions

These conventions take precedence over the skill's blanket rules:

- Internal slices/maps may use their zero value, lazy allocation or `nil` to clear
  state. Initialise maps before writing. At JSON boundaries, preserve the existing
  empty-array versus nullable-field contract.
- Compact `XY{x, y}` literals are the established coordinate notation. Use keyed
  fields for identity references, lifecycle messages and other struct literals.
- Verbatim test-fixture payloads may exceed the line-length guideline. Preserve
  their bytes; wrap the surrounding Go expressions and calls.
- Keep expensive conditions lazy and protect dereferences with their existing nil
  guards when naming compound conditions.

These exceptions avoid changes that add noise or alter state/output merely
to satisfy a style rule. Document any further exception beside the affected code
when its reason is not obvious.
