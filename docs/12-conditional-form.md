# Conditional Parameter Form

claims#115, based on the parameter conditions of claim-machinery-api#135.

## Parameter conditions

A ClaimTemplate parameter can carry two optional fields. Older APIs do not
send them; the form is then the same as before.

```yaml
- name: size
  enum: [small, medium, large, xlarge]
  default: medium
  enumWhen:              # <controlling param>: {<its value>: [allowed values]}
    use_case:
      dev: [large, xlarge]
      rke2: [medium, large, xlarge]

- name: kind_workers
  default: "1"
  showWhen:              # <controlling param>: [values that make this one apply]
    use_case: [kind]
```

The rules are the API's (`internal/conditions` mirrors
claim-machinery-api `internal/claimtemplate/conditions.go`):

- `showWhen`: every key must match (AND), within a key any listed value (OR).
  The controlling value is the one given, else its default; a multiselect
  controller matches when one of its selected values is listed. An inactive
  parameter has no value for other conditions, so chains resolve. Values
  compare as strings (`true`/`false` for booleans).
- `enumWhen`: narrows the enum; a controlling value that is not listed leaves
  the enum as it is; several controllers intersect.

## Interactive form (`claims render`)

- Fields follow the order of `spec.parameters`, which is the decision order.
- A parameter with `showWhen` is on a page of its own (consecutive ones with
  the same condition share it); the page is skipped while the condition does
  not match the answers so far, and its value is not sent.
- A select with `enumWhen` offers only the allowed values, computed from the
  earlier answers (on the same page too). The current value stays if it is
  still allowed, else the first allowed one is selected. `🎲 Random` picks
  among the allowed values.
- Every field is validated before the form can be submitted: required,
  enum (narrowed), pattern, min/max length, integer. If anything still fails
  after the form, the problems are listed and the form opens again with
  `✗ <problem>` next to the field.
- If the API answers `400` with field errors, the review shows one line per
  field; **Edit a template's parameters** (also offered for failed renders)
  reopens the form with the previous answers and the errors next to the fields.
- The form is sized to the terminal before it starts. Before, its first
  frame was drawn at 80 columns and full height; a page taller than the
  terminal (e.g. five fields including a 13-option multi-select) scrolled,
  and the top of that frame stayed on screen as a static copy above the live
  form whenever it was flushed before the terminal size arrived.

## Non-interactive (`--non-interactive`, `--stdout`)

- Values of inactive parameters are not sent; each is noted on stderr:
  `Not sending kind_workers: only applies when use_case is kind`.
- API errors are shown readably:

```text
Rendering native-vm-labul-proxmox...
  ERROR: API returned 400: invalid parameters
      size: use_case dev allows large or xlarge, got small
```

  A failing KCL `assert` (`400 {"error": "<message>"}`) shows the message;
  older APIs' `500` bodies are shown without JSON escapes and ANSI codes.
