import { readdir, readFile, writeFile } from 'node:fs/promises'
import { join } from 'node:path'
import { gzipSync } from 'node:zlib'

async function compressDirectory(directory: string): Promise<void> {
  for (const entry of await readdir(directory, { withFileTypes: true })) {
    if (entry.name.startsWith('.')) continue
    const path = join(directory, entry.name)
    if (entry.isDirectory()) {
      await compressDirectory(path)
    } else if (/\.(js|css|svg|json)$/u.test(entry.name)) {
      await writeFile(path + '.gz', gzipSync(await readFile(path), { level: 9 }))
    }
  }
}

await compressDirectory('.tmp/ui')
