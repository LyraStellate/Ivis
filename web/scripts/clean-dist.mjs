// dist は go:embed の対象なので、ディレクトリ自体は常に存在していなければ
// ならない。vite の emptyOutDir に任せると .gitkeep ごと消えて、新規クローンで
// Go のビルドが通らなくなる。中身だけを消す。
import { readdirSync, rmSync, mkdirSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { join } from 'node:path'

// URL.pathname は Windows で "/D:/..." を返し、fs はこれを相対パスとして
// 扱って "D:\D:\..." を作ろうとする。fileURLToPath を通す。
const dist = fileURLToPath(new URL('../dist/', import.meta.url))
mkdirSync(dist, { recursive: true })

for (const name of readdirSync(dist)) {
  if (name === '.gitkeep') continue
  rmSync(join(dist, name), { recursive: true, force: true })
}
