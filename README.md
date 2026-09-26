# callorder

`callorder` checks the order of functions and methods within each non-generated
Go file. It reports a misplaced declaration and offers a suggested fix that
reorders declarations with their comments and directives. It leaves type,
constant, and variable declarations in place.

## Ordering rules

- A direct call to an unexported function or method in the same file puts its
  caller before the callee. Calls in closures count for the enclosing function.
  Dynamic calls and calls across files do not impose an order.
- Helpers follow their callers depth-first, in first-call order. A helper used
  by several callers follows the last caller; unrelated entry points keep
  their source order.
- Mutually recursive functions stay together, in their existing relative order
  unless a rule below requires otherwise. A call to an exported function that
  a rule places earlier groups the two the same way: a method that calls its
  type's constructor moves up to follow it.
- `init` functions keep their execution order.
- The enabled [settings](#settings) add `decorder` and `funcorder` rules, which
  take priority when call dependencies conflict: a helper declared above a type
  stays there even when a constructor below the type calls it.

A diagnostic names the rule a misplaced function breaks, for example
`expected A before b: exported methods come before unexported methods
(funcorder struct-method)`, or reports a break in depth-first call order.

## Settings

The settings mirror `decorder`'s init-first check and `funcorder`'s checks,
with the same defaults as golangci-lint. Give them the same values as those
linters so that fixes satisfy all three.

| Setting | Default | Rule |
| --- | --- | --- |
| `init-first` | `false` | `init` functions come before other functions. Mirrors `decorder`'s `disable-init-func-first-check: false`. |
| `constructor` | `true` | Constructors (exported `New*` or `Must*` returning a type declared in the file) stay after that type and before its methods. |
| `struct-method` | `true` | Exported methods come before unexported methods of the same type. |
| `alphabetical` | `false` | A type's constructors, exported methods, and unexported methods are each sorted by name. As in `funcorder`, constructors are sorted only with `constructor`, and methods only with `struct-method`. |
| `function` | `false` | Exported functions, including constructors, come before unexported functions. Methods and `init` are exempt. |

Fixes only move functions, so `decorder`'s declaration order and count checks
are unaffected. Constructors already above their type may stay there, since
`funcorder` reports them, while other functions are still ordered. `callorder`
skips a file that only moving a type could fix, such as one with a method
above its type and the constructor below it; `funcorder` reports it too.

The tests in `testdata/src` check each fix against `funcorder` and `decorder`
at the versions golangci-lint v2.13.2 pins; update them together.

## Standalone

From this repository, run `go run ./cmd/callorder ./...` to audit or
`go run ./cmd/callorder --fix ./...` to rewrite files. For JSON diagnostics,
use `go run ./cmd/callorder -json ./...`. You can also build or install the
binary with `go build -o callorder ./cmd/callorder` or
`go install ./cmd/callorder`. Settings are flags, for example
`go run ./cmd/callorder -init-first -constructor=false ./...`.

## golangci-lint module plugin

Use golangci-lint v2. Build a custom binary with a `.custom-gcl.yml` file in
the project that will run the linter:

```yaml
version: v2.13.2
plugins:
  - module: github.com/tho/callorder
    import: github.com/tho/callorder/plugin
    version: vX.Y.Z
```

Run `golangci-lint custom` to create `custom-gcl`. Configure that binary in
the project's `.golangci.yml`:

```yaml
version: "2"
linters:
  enable:
    - callorder
  settings:
    custom:
      callorder:
        type: module
        description: Require depth-first call order within Go files.
        settings:
          init-first: true
```

Run `./custom-gcl run ./...` to audit or `./custom-gcl run --fix ./...` to
apply fixes. Omitted settings keep their defaults; unknown settings fail. A
published private module needs normal Go private-module access (`GOPRIVATE`
and Git credentials) while building the custom binary.
