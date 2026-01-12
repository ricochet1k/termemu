# Kitty Keyboard Protocol Plan

## Goals

- Add protocol state and parsing for Kitty keyboard enhancements (set/query/push/pop).
- Expose richer key events (modifiers, event types, alternates, text codepoints).
- Encode key events using Kitty protocol when requested; preserve legacy behavior otherwise.
- Keep behavior stable with per-screen mode stacks and reasonable limits.

## Phases

1. **Protocol state + parsing**
   - Track keyboard enhancement flags per screen with independent stacks.
   - Parse and apply:
     - `CSI = flags ; mode u` (set flags with mode 1/2/3)
     - `CSI ? u` (query -> reply `CSI ? flags u`)
     - `CSI > flags u` (push current flags, then set to flags; default flags=0)
     - `CSI < n u` (pop N entries; default N=1; empty -> reset)

2. **Key event model**
   - Extend `KeyEvent` to carry:
     - `Event` (press/repeat/release)
     - full modifier bitfield (shift/alt/ctrl/super/hyper/meta/caps/num)
     - optional `Shifted`, `BaseLayout`, and `Text` codepoints for alternates/text reporting
   - Keep existing `KeyCode` set, but allow future expansion for keypad/media keys.

3. **Encoding logic**
   - Add Kitty-aware encoding helpers:
     - modifiers field with optional `:event`
     - alternate key codes (`code:shifted:base` or `code::base`)
     - text-as-codepoints parameter
   - Use Kitty encoding when flags request it:
     - `disambiguate` for Esc + modified ASCII
     - `report_events` for repeat/release
     - `report_all_keys` + `report_text` for text key events
   - Preserve legacy encoding when no flags are enabled.

4. **Tests**
   - `CSI =` set modes and query response.
   - push/pop semantics with per-screen stacks.
   - basic key encoding in Kitty mode (modifiers + event types).

## Notes

- Use the spec’s modifier encoding: `1 + bitfield`.
- Keep main/alt screen stacks independent as required by the protocol.
- Default to legacy behavior for Enter/Tab/Backspace unless `report_all_keys` is set.
