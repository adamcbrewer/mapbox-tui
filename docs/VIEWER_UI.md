# Viewer UI patterns

Use these conventions when adding a panel, dialog or action to the terminal viewer.

| Element | Convention |
| --- | --- |
| Panel or dialog | Keep persistent map tools in the Context, Files or Journey tabs. Use a titled dialog for a short task. When opening a child from a dialog, show the parent in the title breadcrumb and provide an Escape route back. |
| Sections | Give a section a heading and one blank line between groups. On short terminals, spend the available rows on the input and focused choice before adding a description or spacer. |
| Shortcuts | Put local shortcuts below the body, in muted text. Write the key followed by its action, as in `enter inspect / esc close`. Keep shortcuts for a specific command beside its label when there is room. Show only actions that work in the current state. |
| Lists and pickers | For a short choice such as speed or theme, use a single-column list. Render ordinary choices with `choiceRow`. Reserve two cells for the focus marker and highlight the whole focused row. Label the applied value separately when previewing a change. Keep the focused option visible when scrolling. |
| Long names and paths | Measure terminal cells. Fit ordinary labels with a trailing ellipsis. For a filename or path, use `abbreviatedWorkspacePath` so the extension and nearest directories stay visible. Reserve space for indentation and focus markers first. |
| Confirmation buttons | Use `confirmationButtons` for a group of actions. Put Cancel first. Focus the safe default, mark a focused destructive action with the highlight colour, and keep a visible focus marker. Right-align buttons with two cells between them and stack them when the row is too narrow. Use the returned layout’s hit areas for mouse selection. |
| Feedback | Keep input errors beside the field. Show progress and success in the footer. Use theme styles for text, selection and emphasis so a theme change applies across views. |

The shared controls live in [dialog_components.go](../go/dialog_components.go).
The [dialog renderer](../go/ui.go) owns the title, body and footer layout.
