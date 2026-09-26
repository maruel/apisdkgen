# apisdkgen

Independent Go API SDK generator. Keep project-specific CLI entry points in consumer repositories. Run `make fix`, `make verify`, and `make test` after changes. Do not import consumers from this module; specifications and output paths belong to them.

Mandatory: Run `make git-hooks` to install git hooks before making any change.
