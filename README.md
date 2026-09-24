# Loadline

Repository foundation for Loadline.

## Structure

- `apps/web` — minimal Next.js and TypeScript application
- `apps/simulator` — minimal Go module
- `packages`, `providers`, `scenarios`, `proto`, `tests`, `docs`, `docker`, `.github` — reserved project directories

## Requirements

- Node.js 22+
- pnpm 9+
- Go 1.23+

## Commands

```sh
pnpm install
pnpm build
pnpm typecheck
go work sync
go build ./apps/simulator
```
