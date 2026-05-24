---
name: spec-to-mission
description: >
  Reads an existing docs/specs/<slug>/spec.md and all referenced documentation,
  then generates the mission/ artifacts: validation-contract.md, features.json,
  and knowledge-base.md. Use when a spec already exists but has no mission/
  folder — bridges the gap between spec authoring and mission execution.
---

# /spec-to-mission

Takes a confirmed spec and produces the mission artifacts the orchestrator and
workers need. This skill does NOT write code — it reads and decomposes.

## Input

A spec folder path: `docs/specs/<slug>/`

The folder MUST contain `spec.md`. It MAY contain `designs/`, `design-prompt.md`,
or `implementation-plan.md` — read all of them for context.

## When NOT to use

- No `spec.md` exists — run `start-spec` or `mission-spec` first.
- `mission/features.json` already exists and has features — use `mission-orchestrator` directly.
- Spec `status: draft` with unresolved Open Questions marked as blockers — resolve first.

## Pre-flight (run BEFORE generating anything)

1. Read `CLAUDE.md` at the project root — understand stack, architecture, hard rules, inviolable constraints.
2. Read the full `docs/specs/<slug>/spec.md` — every section, every table, every detail.
3. Read `docs/specs/<slug>/design-prompt.md` if it exists.
4. Read `docs/specs/<slug>/implementation-plan.md` if it exists.
5. Scan `docs/specs/<slug>/designs/` — note any screenshots referenced.
6. Read the project codebase structure to understand existing patterns, modules, and conventions.
7. If the spec references other specs, ADRs, or external docs — read those too.

You need a DEEP understanding of the spec before generating artifacts. Shallow
reading produces shallow features — which is exactly the problem this skill solves.

## Workflow

### 1. Analyze the spec

Extract and internalize:

- **Goal** — what is being built and why.
- **Functional Requirements** — each numbered item. These are your primary input.
- **Non-Functional Requirements** — performance, a11y, error handling, security.
- **Data** — API endpoints, Zod schemas, query keys, database schema.
- **State** — form state, URL state, client state.
- **Auth & Tenancy** — who can access, tenant scoping.
- **Scope** — which files/directories are touched.
- **Surface** — routes, components, exports.
- **Open Questions** — note any that affect decomposition.
- **Non-goals / Out of Scope** — boundaries to respect.

### 2. Derive validation-contract assertions

For EVERY functional requirement and non-functional requirement, derive one or
more black-box assertions. This is the most important step — assertions are the
source of truth for "done".

**Rules:**

- Each assertion describes an **observable behavior from the outside** — input +
  action + expected output/state.
- NEVER reference implementation details (class names, function names, variable
  names, file paths). The validator must be able to verify without reading source.
- Format: `<category>.<N>: <precondition/input> → <action> → <observable result>`
- Categories: `api`, `ui`, `data`, `auth`, `error`, `perf`, `a11y`
  (add domain-specific categories if needed, e.g. `wizard`, `form`).
- IDs are scoped per category, starting from 1.
- If the spec has a schema table (like `catalog.event` columns), derive `data.*`
  assertions for required fields, constraints, and relationships.
- If the spec lists API endpoints, derive `api.*` assertions for each endpoint's
  happy path AND error cases.
- Non-functional requirements become assertions: latency thresholds, a11y
  behaviors, error handling patterns, tenant isolation.

**Quality bar:**

- A vague requirement like "user can manage events" is NOT testable — push back
  or decompose it into concrete behaviors.
- Every assertion must be independently verifiable by a fresh validator session
  that has never seen the implementation.
- If a functional requirement has 3+ distinct behaviors, it should produce 3+
  assertions, not one vague one.

**Example conversions from a spec:**

```
FR: "User enters name (required, text input, max 255 chars)"
→ ui.1: Empty name field → submit step 1 → validation error shown, step does not advance
→ ui.2: Name exceeding 255 chars → field enforces max length or shows validation error
→ data.1: Event created with name field matching user input exactly

FR: "slug is auto-derived from name (kebab-case)"
→ ui.3: Type "My Event Name" in name field → slug field shows "my-event-name" automatically
→ ui.4: User can manually override the auto-derived slug

FR: "Slug must be unique within tenant — validated on blur"
→ api.1: POST /api/events with duplicate slug → 409 response
→ ui.5: Slug field blur with existing slug → inline error "slug already taken"

NFR: "Step transitions < 200ms"
→ perf.1: Click "Next" on any step → next step renders within 200ms

NFR: "All form fields have visible labels"
→ a11y.1: Every input in the wizard has an associated visible <label> element
```

### 3. Decompose into features

Break the spec into implementation features. Quality of decomposition determines
quality of execution.

**Principles:**

- Each feature should be completable in **one worker session** (1-3 functional
  requirements max, 3-8 assertions).
- Each feature must be **independently validatable** — its assertions can be
  tested without other features being done (unless declared in `depends_on`).
- Order by **dependency** — schemas before hooks, hooks before components,
  infrastructure before consumers.
- A feature with more than 8 `validation_refs` is too big — split it.
- A feature with 0 `validation_refs` has unclear scope — every feature must be
  validatable.

**Standard decomposition pattern:**

```
Phase 0 — Foundation (no dependencies)
  F01: Zod schemas + TypeScript types
  F02: MSW mock handlers + mock data

Phase 1 — Core (depends on Phase 0)
  F03: TanStack Query hooks (one per API domain)
  F04: Main component/page (layout, routing, navigation)
  F05: Forms + validation (per step or form group)

Phase 2 — Integration (depends on Phase 1)
  F06: Cross-cutting (autosave, state sync, error handling)
  F07: Sub-components (pickers, specialized inputs)

Phase 3 — Polish (depends on Phase 2)
  F08: A11y compliance (keyboard nav, ARIA, focus management)
  F09: Performance (virtualization, lazy loading, debounce)
  F10: Tests + Storybook stories
```

Adjust the number and granularity to match the spec's complexity. A 5-FR spec
needs 3-4 features. A 33-FR spec (like an event wizard) needs 8-12.

**Feature scope must be specific:**

BAD: "Implement step 1 of the wizard"
GOOD: "RHF form with Zod resolver for EventBasicsSchema (name, slug, description,
category picker, performer picker). Auto-derive slug from name. Validation on
required fields. Inline create for category and performer via POST endpoints."

**Feature ID format:** `F01`, `F02`, etc.

### 4. Seed knowledge-base

Create an initial knowledge-base entry documenting:

- Key architectural decisions from the spec.
- Open questions that affect implementation.
- External references mentioned in the spec.
- Stack/pattern constraints from CLAUDE.md that workers must follow.

### 5. Output

Output ONLY a valid JSON object (no markdown, no explanation, no code fences)
matching this exact schema:

```json
{
  "slug": "<from-spec-frontmatter-id>",
  "spec": "docs/specs/<slug>/spec.md",
  "project": "<from-spec-title-or-CLAUDE.md>",
  "owner": "<from-spec-crew_lead-or-created_by>",
  "features": [
    {
      "id": "F01",
      "title": "<concise title>",
      "phase": 0,
      "depends_on": [],
      "scope": "<detailed description of what to implement — specific enough for a worker with no context>",
      "validation_refs": ["data.1", "data.2", "api.1"]
    }
  ],
  "assertions": [
    {
      "category": "ui",
      "items": [
        "ui.1: Empty name field → submit step 1 → validation error shown, step does not advance",
        "ui.2: Name exceeding 255 chars → field enforces max length or shows validation error"
      ]
    }
  ],
  "knowledge": [
    "Rich text editor library TBD — ADR required before F05 implementation",
    "All API calls are MSW-backed, gated by VITE_USE_API_MOCKS",
    "Capacity is derived from seating chart unless pure-GA, then user-editable"
  ]
}
```

**Rules:**

- `slug` MUST match the spec's frontmatter `id` field.
- Every functional requirement must map to at least one assertion.
- Every assertion must be referenced by at least one feature's `validation_refs`.
- Every feature must have at least one `validation_refs` entry.
- Feature `scope` must be detailed enough that a worker with NO prior context
  can implement it by reading only the scope + the spec + the validation contract.
- `depends_on` must be accurate — if F04 uses hooks from F03, declare it.
- Output ONLY the JSON. No explanation, no markdown, no code fences.

## Anti-patterns

- **Shallow spec reading.** If you didn't read the Data section's schema table,
  your assertions will miss field constraints. Read EVERYTHING.
- **One assertion per requirement.** Most requirements have multiple observable
  behaviors — happy path, error case, edge case. Derive them all.
- **Implementation-leaked assertions.** "EventService.create() returns entity"
  is wrong. "POST /api/events with valid data returns 201 with created event" is right.
- **Features without enough scope detail.** "Build step 1" tells a worker nothing.
  List the fields, the schema, the validation rules, the API calls.
- **Ignoring non-functional requirements.** A11y, perf, and error handling are
  real features with real assertions — don't lump them as "polish".
- **Skipping Open Questions.** If the spec has unresolved questions that affect
  decomposition, note them in knowledge and flag features as potentially blocked.
