// dist は go:embed の対象なので、ディレクトリ自体は常に存在していなければ
// ならない。vite の emptyOutDir に任せると .gitkeep ごと消えて、新規クローンで
// Go のビルドが通らなくなる。中身だけを消す。
import { readdirSync, rmSync, mkdirSync } from 'node:fs'
import { join } from 'node:path'

const dist = new URL('../dist/', import.meta.url).pathname
mkdirSync(dist, { recursive: true })

for (const name of readdirSync(dist)) {
  if (name === '.gitkeep') continue
  rmSync(join(dist, name), { recursive: true, force: true })
}
