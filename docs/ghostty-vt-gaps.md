# VT feature gaps vs Ghostty

A sequence-by-sequence comparison of termemu's emulation core against
Ghostty's, with real-world usage measurements where those measurements are
actually meaningful.

**Compared:** termemu @ `d188d6b` (2026-10-26) vs ghostty `main` @ `6ba1441`
(2026-10-28).

**Sources:**

- Ghostty: <https://ghostty.org/docs/vt/reference> plus the implementation
  (`src/terminal/stream.zig`, `src/terminal/osc.zig`, `src/terminal/modes.zig`,
  `include/ghostty/vt/modes.h`). The published reference page is explicitly a
  work in progress, so the source trees are the authority.
- Usage: [qwerasd205/asciinema-stats](https://github.com/qwerasd205/asciinema-stats),
  statistics over 2 years (Nov 2023 – Nov 2025) of *public* asciinema uploads,
  collected by feeding the output of every cast through Ghostty's parser and
  counting stream actions. Split into primary-screen and alternate-screen usage.
  Corpus size: 48.4M CSI / 232k OSC / 1.21B actions on the primary screen and
  70.0M CSI / 1.97M OSC / 541M actions on the alternate screen.

**Scope:** emulation only — parser, screen state, replies. Renderer, UI,
selection/search, and config are out of scope; Ghostty's `libghostty-vt` is the
fair counterpart.

**Decided non-goals:** sixel (unused in practice and superseded by the kitty
graphics protocol; termemu will never advertise DA1 attribute 4), `DECUDK`,
DRCS/`DECDLD`. Legacy xterm-only sequences are not pursued just because xterm
has them — see [Oracles](#oracles).

**Legend:** ✅ supported · ⚠️ partial · ❌ missing · 💤 deliberate non-goal

---

## Which measurements mean something

A cast is a recording of the bytes the program wrote, so anything the program
emits appears in it — asciinema's own rendering does not filter anything. What
does distort counts is **what programs agree to emit**, and that splits the
sequences into three classes. Every popularity cell below is tagged with the
class, because a raw number is only meaningful for the first one.

| Class | What it means | How to read the number |
|---|---|---|
| **`R`/`S` (plain)** | App-initiated output with no capability handshake: text, SGR, cursor/erase, DECSLRM, `HPA`, `OSC 1`, title stack, `XTSAVE`, protected cells, rect ops, `DECID`, `SS2`/`SS3`, `OSC 8`/`52`/`133`. | The count is the count. A zero here is strong evidence of disuse. |
| **`gated`** | Programs only emit these after detecting support (handshake or `$TERM`): kitty graphics, kitty keyboard modes, `OSC 21`/`22`, `OSC 66`/`72`/`99`/`3008`/`5522`, `?2027`, `?2048`, iTerm2 images. The recorder answers the detection, not your terminal. | A **floor**, never a prevalence. Zero proves nothing. |
| **`reply-only` / `not counted`** | Terminal→app reports (DECRPM, DECRQSS replies, mode reports) never appear in a cast at all; and the archive's analyzer predates some OSC numbers, so an absent row means "not counted", not "zero". | Not measurable this way. Requests the app makes *are* measurable; replies are not. |

Measurements:

- **`R`** — the 2-year archive, occurrence count with the percentage of that
  category (CSI vs CSI, OSC vs OSC, actions vs actions).
- **`S`** — a 27-recording sample taken while writing this doc (6.8 MB of
  output, from <https://asciinema.org/explore>), counted locally, reported as
  "casts using it / 27 (occurrences)". It is recent, so newer protocols appear
  here when the archive's parser never knew about them, but 27 casts is a weak
  sample.

Other caveats: counts are occurrences summed over all recordings, so one noisy
program can dominate a row (`R` answers "does anything send this, and roughly
how often", not "how many programs need it"); casts are mostly developer demos,
so interactive/image/mouse-heavy workflows are under-represented.

---

## Headline

Over 1.75B recorded stream actions, the gaps sort like this:

- **Measured, and worth doing:** DECSLRM (64k, likely one dominant producer but
  the parse conflict is real), `OSC 1` (14.6k), title stack (11.7k), DA2
  queries (1.4k), DECRQM requests (1.1k).
- **Measured, cheap, cold:** `XTSAVE`/`XTRESTORE` (513), `OSC 52` (525),
  `CSI k` (432), `HPA` (323), `OSC 9;4` (303), XTVERSION (587), `DECSTR`
  (175). Do these opportunistically; none is urgent.
- **Measured dead (ungated, so the zero is real):** protected cells (3),
  rectangular ops (3), all checksums (absent), `SS2`/`SS3` (1), `DECID` (3),
  `LS1R`/`LS2R`/`LS3R` (3), `DECALN` (0).
- **Unknown by construction:** kitty graphics, `OSC 21`/`22`, `OSC 66`/`72`/
  `99`/`3008`/`5522`, `?2027`, `?2048`. These are capability-gated or invisible
  to cast recording, so the corpus cannot rank them. Decide them on "does the
  `Frontend` API have anywhere to put it", not on popularity.
- **SGR `4:x` underline styles** are in the middle: ungated, but only ~1.9k
  *ambiguous* colon-SGR sequences in two years and zero in the recent sample.

---

## Priority list

Ordered by how much *usable* evidence there is. "Cost" is rough effort in this
repo.

| # | Gap | Popularity (measured class) | Cost | Verdict |
|---|---|---|---|---|
| 1 | `DECLRMM` (`?69`) + `DECSLRM` (`CSI Pl;Pr s`) | `R` 64,176 alt (0.09% of alt CSI); `S` 1/27 (8). Ungated, so real — though probably one dominant program. `CSI Ps;Ps s` is currently mis-parsed as SCOSC. | high | Do it; it is a screen-model change, don't fake it |
| 2 | `OSC 1` (window icon) | `R` 14,556 primary (6.3% of primary OSC). Ungated. | trivial | Do it; share the title/icon field |
| 3 | Title stack `CSI 22;Pt` / `23;Pt` | `R` 11,670 (push 79 primary + 5,732 alt; pop 5,040 primary + 819 alt). Ungated. | trivial | Do it; currently accepted and dropped |
| 4 | DECRQM "permanently set/reset" replies (3/4) | `R` 1,088 DECRQM *requests* (1,086 alt). Replies are reply-only, but the request volume is real. | trivial | Do it |
| 5 | `XTSAVE`/`XTRESTORE` (`CSI ? Ps s` / `? Ps r`) | `R` 513 (234 push + 244 pop alt; 23 + 12 primary). Ungated. | small | Do it; cheap mode save/restore |
| 6 | `CSI Ps k` (CUU alias) | `R` 432 primary. Ungated. | trivial | Do it |
| 7 | `HPA` (``CSI Ps ` ``) | `R` 323 alt, 0 primary; `S` 0/27. Ungated, so genuinely rare but real. | trivial | Do it when nearby |
| 8 | `OSC 9;4` progress report | `R` 303 primary (0.13% of primary OSC). Ungated. | small | Do it; forward-only |
| 9 | `?2031` color-scheme report | `S` 2/27 (72 set + 71 reset). The app's enable is visible; the terminal's report is reply-only. Gated in practice (apps probe first). | small | Do it if the frontend can supply a scheme |
| 10 | SGR `4:3`/`4:4`/`4:5` underline styles | `R` ≤1,871 (2-param colon SGR in alt output; the shape fits `4:x` or a truncated `38:5`); `S` 0/27. Ungated but ambiguous. | small | Defer; only for Neovim undercurl |
| 11 | `?2027` grapheme clusters | `S` 1/27 (1). Gated → floor. | high | Defer: needs live tokenizer switching |
| 12 | `?2048` in-band resize | `S` 0/27. Gated (app sets it, terminal reports are reply-only) → no signal. | medium | Defer |
| 13 | `?45`/`?1045` reverse wrap, `DECBKM` (`?67`), `?66`/`?1035`/`?1036`/`?1039` | `R` no per-mode data; `S` 0/27. Input-side modes with no distinguishing measurement. | small-medium | Defer |
| 14 | DECSED/DECSEL (`CSI ? Ps J/K`) | `R` no row (the archive folds `J`/`K` params together); `S` 0/27. Ungated, so a weak zero. | small | Low |
| 15 | Kitty graphics (APC) / iTerm2 images (`OSC 1337`) | **gated**: `R` 4,736 APC sequences primary + 283 alt (~3.3 MB payload) and 240,343 DCS hooks in alt — a floor, not a prevalence. `S` 0/27 (no images in a text sample). | very high | Only if a renderer can draw images |
| 16 | `OSC 21` kitty colors, `OSC 22` mouse shape | **gated / handshake**: `R` 0/0 observed. Says nothing — both are kitty-detection-dependent. | small | Skip unless the frontend exposes it |
| 17 | `OSC 66` text sizing, `OSC 72` DnD, `OSC 99` notifications, `OSC 3008`, `OSC 5522` | **not counted** by the archive's analyzer (newer than its Ghostty); `S` 0/27. | small-high | Defer |
| 18 | Protected cells (`DECSCA`, `SPA`, `EPA`) | `R` 3 `protected_mode_off` actions in 1.75B. Ungated ⇒ dead. | medium | Skip |
| 19 | Rectangular ops (`DECCRA`/`DECFRA`/`DECERA`/`DECRRA`) | `R` 3 (`DECFRA`) in 1.75B. Ungated ⇒ dead. | medium | Skip |
| 20 | `DECRQCRA` / `XTCHECKSUM` | `R` no rows; `S` 0/27. Ungated ⇒ dead. | medium | Skip |
| 21 | `SS2`/`SS3`, `DECID`, `LS1R`/`LS2R`/`LS3R`, `SPA`/`EPA` | `R` 1, 3, 3, 3. Ungated ⇒ dead. | trivial | Skip |
| 22 | Sixel | **gated** (DA1 attribute 4) and not separable from the DCS total; `S` 2/27 casts sent any DCS at all. | very high | **Out of scope by decision** — unused in practice and superseded by kitty graphics; never advertise DA1 attr 4 |

---

## Where termemu leads

Not gaps to fix — worth keeping so an alignment pass does not regress them:

| Feature | termemu | Ghostty | Evidence |
|---|---|---|---|
| `DCS $q` DECRQSS | `m`, `r`, `s`, ` q`, `t`, `$\|`, `*\|`, `*x` | none found | `R` 240,343 DCS hooks in alt-screen output (tmux sends DECRQSS); replies are reply-only but the traffic is real |
| `OSC 6` (legacy cwd) | mapped to current directory | not handled | Ungated; `R` sees `OSC 7` (9,075 primary) |
| `DECSET ?1046` alt-screen lockout | ✅ | not a mode | Ungated |
| Serializable state | `Snapshot`/`Restore` for screen, modes, palette, charsets, parser state, scrollback | no public round-trip format | n/a |
| Process handoff | `Pause`/`Resume`/`NewFromHandoff` with fd transfer and partial-escape replay | none | n/a |
| Scrollback API | stable `ScrollID`s, logical lines, reflow on read, no-lock view | internal only | n/a |
| Grapheme vs rune mode | chosen at construction | runtime `?2027` | `S` 1/27 for `?2027` |

---

## Detail: CSI

`R` is occurrences out of 48.4M primary / 70.0M alternate CSI. Every row here
is app→terminal output; the ones marked `gated` are the kitty keyboard forms.

| Final | Meaning | `R` | termemu |
|---|---|---|---|
| `A` `k` | CUU | 4,437,395 / 143,345 (`A`); 432 / 0 (`k`) | ✅ / ❌ (`k`) |
| `B` `C` `D` | CUD/CUF/CUB | 558k/892k/250k primary; 197k/1.48M/102k alt | ✅ |
| `E` `F` | CNL/CPL | 6,291/31,873 primary; 3,788/24 alt | ✅ |
| `G` | CHA | 331,613 / 289,467 | ✅ |
| `` ` `` | HPA | 0 / 323 | ❌ |
| `H` `f` | CUP | 2.5M / 13.7M | ✅ |
| `I` `Z` | CHT/CBT | 0 / 0 (`I`); 0 / 172 (`Z`) | ✅ |
| `J` | ED | 106k + 75k / 5.2k + 18k | ⚠️ (0-3; no 4/5) |
| `K` | EL | 1.97M / 2.2M | ✅ |
| `L` `M` | IL/DL | 2,285/334 primary; 19.8k/15.3k alt | ✅ |
| `P` `@` `X` | DCH/ICH/ECH | 29.8k/10.7k/27.2k primary; 128k/2.6k/1.0M alt | ✅ |
| `S` `T` | SU/SD | 119/0 primary; 60.8k/2.8k alt | ✅ |
| `a` `e` | HPR/VPR | 64 / 0 | ✅ |
| `b` | REP | 318 / 37,764 | ✅ |
| `d` | VPA | 10,781 / 624,288 | ✅ |
| `g` | TBC | 17/7 primary; 1/1 alt | ✅ |
| `h` `l` | SM/RM (ANSI) | 1,842/929 primary; 44.0k/7.0k alt | ⚠️ (4, 20 only) |
| `? Pm h` `? Pm l` | SM/RM (private) | 747k/767k primary; 801k/1.36M alt | ⚠️ (subset of modes) |
| `m` | SGR | 34.1M / 45.8M; `4:x` ungated but ≤1,871 ambiguous | ⚠️ (no `4:x`) |
| `n` | DSR | 9,655 / 1,813 (requests) | ⚠️ (5, 6 only) |
| `r` | DECSTBM | 42,448 / 725,752 | ✅ |
| `s` | DECSLRM / SCOSC | 436 / 67,140 (bare); 0 / 64,176 (`Pl;Pr`) | ❌ (always SCOSC) |
| `t` | XTWINOPS | 4,975 / 5,029 (3 param); 164 / 3,120 (2 param) | ⚠️ (14/16/18; 22/23 no-op) |
| `u` | SCORC | 1,651 / 34 | ✅ |
| `?u` | kitty keyboard query | 30 / 474 — app-initiated, so real: something is probing for support | ✅ |
| `>u`, `<u`, `=u` | kitty keyboard push/pop/set | **gated** by the query reply: 26/317, 21/226, 1,322/116 are floors | ✅ |
| `>4m` | modifyOtherKeys | 1,747 / 2,539 | ✅ |
| `$p` | DECRQM | 2 / 1,086 (requests; replies are reply-only) | ⚠️ (no 3/4) |
| `!p` | DECSTR | 172 / 3 | ✅ |
| `" q` | DECSCA | 0 / 0 — ungated ⇒ dead | ❌ (skip) |
| ` q` | DECSCUSR | 8,573 / 68,946 | ✅ |
| `$ v`-`$ t` | rect ops | 0 / 3 (`DECFRA`) — ungated ⇒ dead | ❌ (skip) |
| `? $ y` / `$ u` | DECRQCRA / XTCHECKSUM | absent | ❌ (skip) |
| `> Ps s` | XTSHIFTESCAPE | 0 / 0 — ungated | ❌ (skip) |
| `> Ps c` / `> q` | DA2 / XTVERSION | 33/1,411 and 47/540 (requests) | ✅ |
| `? Ps s` / `? Ps r` | XTSAVE / XTRESTORE | 23/234 and 12/244 — ungated | ❌ (cheap, worth adding) |
| SGR 2-param colon (`4:x` shape) | | 0 / 1,871 — ambiguous | ❌ (cold) |
| SGR 3-param colon (`38;5;n` shape) | | 20,052 / 11,723 | ✅ |
| SGR 5-param colon (`38;2::r;g;b`) | | 1,895 / 538,203 | ✅ |

## Detail: ESC / C0

All ungated.

| Sequence | Meaning | `R` (primary / alt) | Ghostty | termemu |
|---|---|---|---|---|
| `ESC ( ) * +` | charset designation | 545k / 4.7M | ✅ | ✅ |
| `ESC 7` / `ESC 8` | DECSC/DECRC | 40,157/47,121 ; 45,495/45,636 | ✅ | ✅ |
| `ESC # 8` | DECALN | 0 / 0 | ✅ | ✅ |
| `ESC D` `ESC E` `ESC M` `ESC H` | IND/NEL/RI/HTS | 870/2,762/144k/602; 0/8,832/26.7k/18 | ✅ | ✅ |
| `ESC N` `ESC O` | SS2/SS3 | 0 / 1 | ✅ | ❌ (skip) |
| `ESC n` `ESC o` | LS2/LS3 | 3 / 0 | ✅ | ✅ |
| `ESC ~` `ESC }` `ESC \|` | LS1R/LS2R/LS3R | 2/0 ; 1/0 | ✅ | ❌ (skip) |
| `ESC V` `ESC W` | SPA/EPA | 0/0 ; 3/0 | ✅ | ❌ (skip) |
| `ESC Z` | DECID | 3 / 0 | ✅ | ❌ (skip) |
| `ESC =` `ESC >` | DECKPAM/DECKPNM | 15,962/16,905 ; 13,367/2,967 | ✅ | ✅ |
| `ESC c` | RIS | 1,097 / 3 | ✅ | ✅ |
| `ENQ` | answerback | 1,480 / 0 | ✅ | ❌ (skip) |

## Detail: modes

No per-mode numbers exist in the archive: every DEC private mode collapses into
one `set_mode`/`reset_mode` action, so `R` is `—` throughout. The 27-cast
sample (`S`) does resolve mode numbers; given its size, treat `S` as "did any
recent recording ask for this", and note that gated modes (`?2027`, `?2048`,
`?5522`, and anything an app probes for first) produce weak zeros.

Generic traffic for context: `CSI Ps h` (ANSI set) 1,842 primary / 43,971 alt
and `CSI Ps l` 929 / 6,952 — so ANSI *insert mode* (`4h`/`4l`) is exercised
thousands of times; `CSI ? Pm h` 747k / 801k and `? Pm l` 767k / 1.36M.

**ANSI:** Ghostty supports 2 (KAM), 4 (IRM), 12 (SRM), 20 (LNM); termemu has 4
and 20. `S`: `4` reset twice in one cast; `2`/`12` unseen.

**DEC private:**

| Mode | Ghostty | termemu | `S` (casts / 27) |
|---|---|---|---|
| 1 DECCKM | ✅ | ✅ | 9 (13) |
| 3 132-column | ✅ | ❌ | 2 resets, 0 sets — effectively dead |
| 4 smooth scroll | ✅ | ❌ | 0 |
| 5 reverse video | ✅ | ❌ | 0 |
| 6 DECOM | ✅ | ✅ | 0 |
| 7 DECAWM | ✅ | ✅ | 2 |
| 8 autorepeat | ✅ | ❌ | 0 |
| 9 X10 mouse | ✅ | ✅ | 0 |
| 12 cursor blink | ✅ | ✅ | 6 (resets) |
| 25 cursor visible | ✅ | ✅ | 14 |
| 40 allow 132-col | ✅ | ❌ | 0 |
| 45 / 1045 reverse wrap | ✅ | ❌ | 0 |
| 47 / 1047 / 1048 / 1049 | ✅ | ✅ | 1 (`47`) / 11 (`1049`) |
| 66 application keypad | ✅ | ⚠️ (no DECRQM) | 0 |
| 67 DECBKM | ✅ | ❌ | 0 |
| 69 DECLRMM | ✅ | ❌ | 1 (8) — plus 64k DECSLRM actions in `R` |
| 1000/1002/1003/1004 | ✅ | ✅ | 5 / 3 / 2 / 2 |
| 1005/1006/1015/1016 | ✅ | ✅ | 1006 in 2 |
| 1007 alt scroll | ✅ | ✅ | 0 |
| 1034 | ✅ | ⚠️ (no-op) | 3 (xterm "metaSendsEscape") |
| 1035/1036/1039 | ✅ | ❌ | 0 |
| 1046 | (not a Ghostty mode) | ✅ | 0 |
| 2004 bracketed paste | ✅ | ✅ | 7 (86) |
| 2026 synchronized output | ✅ | ✅ (exposed) | 2 (23) |
| 2027 grapheme clusters | ✅ | ❌ | 1 — gated ⇒ floor |
| 2031 color scheme report | ✅ | ❌ | 2 (72 set / 71 reset) — gated in practice |
| 2033 visibility report | ✅ | ❌ | 0 — report is reply-only |
| 2048 in-band resize | ✅ | ❌ | 0 — gated, reports reply-only |
| 5522 paste events | ✅ | ❌ | 0 — gated |

Also observed: `?7727` (3 sets in 1 cast), a probe, not a feature.

## Detail: OSC

`R` is occurrences out of 231,863 primary / 1,971,542 alternate OSC.

| OSC | Ghostty | termemu | `R` / measurement class |
|---|---|---|---|
| 0, 2 | ✅ | ✅ | 65,451 / 6,201 — ungated |
| 1 | ✅ (parsed, ignored) | ❌ | 14,556 / 19 — ungated |
| 4, 104 | ✅ | ✅ | 3,013 / 13,105 — ungated |
| 5, 105 | ✅ | ❌ | not separable from the same counter |
| 6, 7 | ✅ (7) | ✅ (both) | 9,075 / 4 (`7`) — ungated |
| 8 | ✅ | ✅ | 13,895 + 117,229 / 17,837 + 1,933,861 — ungated |
| 9 | ✅ | ✅ | 1,094 / 2 — ungated |
| 9;4 | ✅ | ❌ | 303 / 0 — ungated |
| 10-12, 110-112 | ✅ | ✅ | not separable from `color_operation` |
| 13-19, 113-119 | ✅ | ❌ | not separable from `color_operation` |
| 21 | ✅ | ❌ | 0 / 0 — **gated**, proves nothing |
| 22 | ✅ | ❌ | 0 / 0 — **gated**, proves nothing |
| 52 | ✅ | ✅ (writes only) | 12 / 513 — ungated |
| 66, 72, 99, 3008, 5522 | ✅ | ❌ | **not counted** by the archive's analyzer; `S` 0/27 (gated) |
| 133 | ✅ | ✅ (A/B/C/D) | 2,756 start / 2,247 end / 1,173 in / 1,059 cmd — ungated |
| 777 | ✅ | ✅ | folded into notifications |
| 1337 | ✅ | 💤 | 0 / 0 — **gated** (iTerm2 detection) |
| 7501 | ✅ | ⚠️ (query only) | not counted; `S` 0/27 |

`OSC 8` dwarfs everything on the alternate screen: 98% of all alt-screen OSC
traffic is `hyperlink_end` (1.93M). termemu's per-span `Span.Link` is the right
design — keep it.

## Detail: DCS / APC

| Sequence | Ghostty | termemu | Measurement |
|---|---|---|---|
| `DCS $ q Pt ST` (DECRQSS) | ❌ | ✅ (`m`, `r`, `s`, ` q`, `t`, `$\|`, `*\|`, `*x`) | requests visible inside `dcs_hook`: 393 primary / 240,343 alt; replies are reply-only |
| `CSI > q` (XTVERSION) | ✅ | ✅ | 47 / 540 requests |
| `DCS` + kitty graphics | ✅ | 💤 | gated |
| `APC` (`ESC _`) | kitty graphics; others consumed | consumed | gated: `apc_start` 4,736 / 283, `apc_put` 3.27M / 350k (~3.3 MB payload) — a floor |
| Sixel (`DCS q`) | ❌ | 💤 | out of scope by decision; gated on DA1 attr 4, which termemu must never advertise |

DCS traffic is not negligible: 240k hooks in alt-screen output, almost
certainly tmux (which sends `DCS $q` capability queries and wraps passthrough
payloads). That is a reason to keep DECRQSS and to never leak DCS payloads into
the grid; termemu does both correctly today.

---

## No evidence vs evidence of absence

Recording-based measurement can only kill features that programs send without
asking first. Grouped accordingly:

**Strong evidence of disuse (ungated, zero or ~zero in 1.75B actions):**

- protected cells: `DECSCA`, `SPA`, `EPA` (`protected_mode_*`: 3 total)
- `DECFRA` (3), and by extension `DECCRA` / `DECERA` / `DECRRA` / `DECRQCRA` /
  `XTCHECKSUM`
- `SS2`/`SS3` (1), `DECID` (3), `LS1R`/`LS2R`/`LS3R` (3), `DECALN` (0)
- `XTSHIFTESCAPE`, `DECIC`/`DECDC` (never observed), `DECUDK`, `DRCS`

**No verdict available (gated or invisible):** `OSC 21`, `OSC 22`, kitty
graphics, iTerm2 images, `OSC 66`/`72`/`99`/`3008`/`5522`, `?2027`, `?2048`,
`?5522`, and every terminal→app reply. For these the only real question is
whether the frontend API can express them; the corpus cannot answer it. (Sixel
is not in this group: it is out of scope by decision, not by measurement.)

---

## Capability channels

A program decides what this terminal can do from up to five independent
channels, and they can disagree. Today termemu sets two of them, leaves one
unset, and answers the other two:

| Channel | Where it lives today | Notes |
|---|---|---|
| `TERM` + terminfo | `defaultAutoEnv` in `backend.go` injects `TERM=xterm-256color` | Selects the ncurses entry, which is what gates `Smulx`/`Setulc` (underline styles) and most TUI feature tiers. termemu ships no entry of its own. |
| `COLORTERM` | injected as `truecolor` | Honored by Neovim et al. Keep. |
| `TERM_PROGRAM`/`_VERSION`, `KITTY_WINDOW_ID`, `WEZTERM_*`, `VTE_VERSION`, `LC_TERMINAL`, `GHOSTTY_RESOURCES_DIR` | not set | Modern apps tier features on these and otherwise fall back to `$TERM`. Do **not** set `TMUX`/`TMUX_PANE`. |
| DA1/DA2/`XTVERSION` | `CSI c` → `ESC[?1;2c`; `CSI > c` → `ESC[>1;4402;0c`; `CSI > q` → `termemu(0.1)` | Keep DA1 honest: it is the only way to advertise (or refuse) sixel, and sixel is refused by decision. |
| DECRQM / `CSI ?u` / DECRQSS / colour queries | answered for the modes termemu knows | This is where inconsistency shows up: a mode can be reported as set while `$TERM` says it does not exist. |

Two rules follow:

1. **Channels must agree.** If termemu answers `DECRQM ?1049` and the kitty
   keyboard query, then `TERM=xterm-256color` (no kitty caps, no `Smulx`) is
   understating it; if `?2027` or `?2031` are ever answered as supported, the
   env side has to say so too. A test asserting cross-channel consistency is
   worth more than any single reply test.
2. **Never claim what is not implemented.** Kitty graphics is acknowledged
   (`k=i` replies); a terminal that advertises it and silently discards APC does
   not just render nothing, it can hang the app waiting for the response. Same
   reasoning for sixel via DA1 attr 4.

The clean shape is a `Profile` (or `Capabilities`) value that owns all five
channels together: `AutoEnv`, the terminfo entry (`TERM`/`TERMINFO`), the
DA1/DA2/`XTVERSION` strings, and the mode/query answers — with a documented
default that reflects what is actually implemented. `AutoEnv` stays as the
low-level escape hatch. Profiles are also the cheapest way to measure env-gated
demand: run the same program corpus under `xterm-256color`, `screen-256color`
(what tmux casters see) and a max-capability profile, and diff the emitted
streams.

## Oracles

No single oracle is authoritative; each decides a subset. What each may be
used for:

**tmux — smoke test only.** `tmux_diff_test.go` is fast and catches common-path
regressions in shells, pagers and editors, but tmux re-parses VT into its own
grid and re-emits it, so `capture-pane` reports *tmux's* model. It is not
authoritative for, and should not be extended to cover:

- character sets / DEC graphics (tmux keeps the designation bytes in the cell
  and leaves mapping to the outer terminal),
- `CHT` (unimplemented) or `HPR`/`VPR` (ignored),
- underline styles (`4:x`), underline colour (`58`), dim/rapid-blink — SGR is
  normalized and re-encoded for `-e`,
- rectangle ops, `DECSCA`/selective erase, `DECALN`, `DECRQCRA`,
- tabs (`capture-pane` reports the tab character, not the expansion),
- mid-screen scroll regions (region output interleaves),
- wide/ambiguous widths (tmux has its own table, not uniseg's),
- most modes, `DECRQM`, `XTSAVE`, `DECRQSS`.

Rule: add a case to `tmuxDiffSequences` only when tmux implements the feature
at all. "tmux ignores it" is not a reason to leave a sequence untested — that is
what the oracle below is for.

**libghostty-vt via `go.mitchellh.com/libghostty` — the intended semantic
oracle.** It is the same engine as the terminal this document compares against,
it is headless, and it can format a screen as plain text or VT so two emulators
can be fed one byte stream and diffed. Use it for replaying the corpora,
fuzzed sequences, and any row in these tables. Practical notes:

- It lives in `oracles/ghostty`, its own module (`replace
  github.com/ricochet1k/termemu => ../..`), with the corpus runner alongside
  the harness. That keeps it invisible to the root module's `go build`,
  `go test`, `go vet`, lint and the `GOOS=windows` cross-build; give it an
  opt-in CI job (or none) rather than gating the root matrix.
- It is cgo and needs `libghostty-vt` installed (`PKG_CONFIG_PATH` pointing at
  a checkout; static link by default). The default build is pure Go —
  `replay` works, `diff` exits with a message — so a plain `go build`/`go test`
  in that module is safe everywhere. To enable the comparison:
  `go get go.mitchellh.com/libghostty` and `go build -tags libghostty`; see
  `oracles/ghostty/README.md`. The binding is deliberately absent from
  `go.mod` so the default build needs no network or cgo.
- The Go bindings explicitly do not promise API stability yet.
- It is the reference for *scope*, not for *semantics*. Ghostty declines
  `DECRQSS` (termemu is ahead there) and sixel, so a disagreement means "decide
  by the documented specification", not "Ghostty does X". The `R` numbers in
  this document were produced with the same engine, so its action names and
  parser behaviour are also what the statistics mean.

**Replay corpus.** Feed recorded cast data and shared fuzz seeds into both
termemu and libghostty-vt and diff the formatted screen, cursor, styles and
modes — at the end of the stream *and* after chunk boundaries, which also
exercises `ReaderState`/handoff. This lives in the oracle module with the
harness, so the root module gains no cgo or external-library dependency.

**Deliberately not used:** **xterm** (huge, very old, implements sequences
almost nothing emits; being byte-compatible there would force support for dead
features, and headless grid extraction is painful), **libvterm**, **VTE** and
**esctest** (esctest encodes xterm's expectations, so it would import the same
bias). If one of these is ever run, treat it as a bug-finder, not as a pass/fail
gate.

**Tie-breaks:** when two oracles disagree, decide from documentation — xterm's
ctlseqs *as a written spec*, the kitty protocol documents, DEC VT manuals — and
record the citation in the test. Never pick an implementation because it is
convenient.

## Implementation map

- **New CSI finals** (`k`, `HPA`, `XTSAVE`/`XTRESTORE`, `DECSLRM`):
  `handleCommand`'s `prefix == 0` and `prefix == '?'` switches in `escapes.go`.
  Add fast-path cases in `tryFastCSI` only for hot finals (`H`/`f`, `m`,
  `A`-`D`, `J`, `K` currently).
- **DECSLRM/DECLRMM**: `screen.go` (`spanScreen`) plus every scroll/erase/
  insert path, and `terminal.go` for the DECRQM state. `scrollCapture` must
  keep capturing only full-width, top-of-screen scrolls.
- **XTSAVE/XTRESTORE**: a per-mode saved stack in `terminal.go`; the `?h`/`?l`
  switch is already centralized, so this is mostly bookkeeping.
- **SGR `4:x`** (if ever): `style.go` (`Mode` constant, `modeSGRCodes`,
  `modeSGRGroups`, `appendANSIEscape...`) plus the colon-group parsing in the
  `m` handler; `TestModeSGRCodesCoverAllModes` enforces the bookkeeping.
- **New OSCs** (`1`, `9;4`): the `handleOSC` switch in `escapes.go`; follow
  `NotificationSink`/`ClipboardSink` for anything a frontend must see,
  `handleOSCDynamicColor` for anything answered over the wire. `OSC 1` should
  share the title field.
- **Mode 2031**: a new `ViewFlag`/`ViewInt` set by the frontend plus the reply
  path; also needs DECRQM reporting.
- **Graphics**: no place to put it yet — a pin/image model in `screen.go`,
  lifecycle in `scrollback.go`, a `Frontend` extension, and snapshot/handoff
  encoding covered in `serialize_test.go`.

## Verifying

- Unit tests per sequence in `escapes_test.go`; replies in
  `escapes_replies_test.go`; mode round-trips in `modes_cursor_test.go`.
- Screen-model changes (margins, rect ops) must keep `checkScreenInvariants`
  passing and should run through `FuzzTerminalInput` /
  `FuzzTerminalInputChunking`, which already round-trip `Snapshot`/`Restore`.
- Anything in the tables above should be added to the ghostty oracle corpus in
  `oracles/ghostty` rather than only to `tmuxDiffSequences`. The tagged build
  that actually runs the comparison is unverified in this environment (no Zig
  or `libghostty-vt`), so treat the first real `diff --cast` run as the
  acceptance test for that path.
- Cross-channel capability consistency (see Capability channels) deserves its
  own test: `$TERM`/terminfo, DA1/DA2/`XTVERSION` and the `DECRQM`/query
  answers must describe the same terminal.
- Sequences that programs send only after capability detection cannot be ranked
  by the corpus; test them against the actual program (e.g. `kitten icat`,
  `nvim` undercurl, tmux passthrough) or A/B the capability profile.

## Keeping this doc current

Re-derive Ghostty's columns from `src/terminal/stream.zig`,
`src/terminal/osc.zig`, `src/terminal/modes.zig` and
`include/ghostty/vt/modes.h`, and update the commit ids at the top. The `R`
numbers come from the published TSVs in
[qwerasd205/asciinema-stats](https://github.com/qwerasd205/asciinema-stats)
(`primary/` and `alternate/`: `csi.tsv`, `esc.tsv`, `osc.tsv`, `actions.tsv`);
keep the measurement-class tags when you copy numbers, since most of the
interesting gaps are the untaggable ones. The `S` sample was 27 casts from
<https://asciinema.org/explore> (6.8 MB of output); redo it the same way if you
need fresher per-mode numbers.
