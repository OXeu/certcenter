import { mkdir, readFile, writeFile } from 'node:fs/promises'
import { createDocsRenderer } from './render-docs.ts'

const renderer = await createDocsRenderer()
try {
  await mkdir('.tmp/docs', { recursive: true })
  await Promise.all(
    ['index', 'api', 'sdk', 'mcp', 'config', 'operations'].map(async (name) => {
      const source = await readFile(`docs/${name}.md`, 'utf8')
      await writeFile(`.tmp/docs/${name}.html`, await renderer.render(source))
    }),
  )
  await writeFile('.tmp/docs/shiki.css', renderer.css())
} finally {
  renderer.dispose()
}
