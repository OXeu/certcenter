import { describe, it, expect } from 'vite-plus/test'
import { readFileSync, readdirSync } from 'node:fs'
import { parse } from '@vue/compiler-sfc'
import { checkDangerTemplate } from '../../../tools/oxlint/danger.ts'
import { checkIconLabels } from '../../../tools/oxlint/icon-labels.ts'
import { checkTemplate } from '../../../tools/oxlint/templates.ts'
import { fallbackMessages } from '../src/i18n.ts'
const locales = ['en', 'zh-CN'].map((lang) =>
  JSON.parse(readFileSync(`internal/i18n/locales/${lang}.json`, 'utf8')),
)
describe('Vue template rules', () => {
  it.each([
    ['<input />', 'input-outside-dialog'],
    ['<svg />', 'inline-icon'],
    ['<dialog><select /></dialog>', 'no-native-select: use DropdownSelect instead'],
    ['<dialog><Selector /></dialog>', 'no-native-select: use DropdownSelect instead'],
    ['<dialog><component is="select" /></dialog>', 'no-native-select: use DropdownSelect instead'],
    [
      '<dialog><component :is="\'select\'" /></dialog>',
      'no-native-select: use DropdownSelect instead',
    ],
    ['<DropdownSelect />', 'input-outside-dialog'],
    ['<p role="alert">error</p>', 'inline-message'],
    ['<div v-html="value" />', 'unsafe-html'],
    ['<a href="javascript:alert(1)">link</a>', 'unsafe-url'],
    ['<button />', 'unnamed-control'],
    ['<button @click="document.querySelector(\'x\')">x</button>', 'raw-dom'],
  ])('rejects %s', (source, rule) => expect(checkTemplate(source)).toContain(rule))
  it('accepts inputs in native dialogs', () =>
    expect(checkTemplate('<dialog><label>Name<input /></label></dialog>')).toEqual([]))
  for (const file of readdirSync('web/ui/src').filter((name) => name.endsWith('.vue'))) {
    it(`validates ${file}`, () => {
      const source = readFileSync(`web/ui/src/${file}`, 'utf8')
      const { descriptor } = parse(source)
      expect(checkTemplate(descriptor.template!.content, `web/ui/src/${file}`)).toEqual([])
      expect(
        checkIconLabels(
          descriptor.template!.content,
          descriptor.scriptSetup?.content ?? '',
          fallbackMessages,
        ),
      ).toEqual([])
      expect(checkDangerTemplate(descriptor.template!.content, `web/ui/src/${file}`)).toEqual([])
      expect(source.split('\n').filter((line) => line.trim()).length).toBeLessThanOrEqual(400)
      for (const match of source.matchAll(/msg\('([^']+)'/gu)) {
        for (const [index, locale] of locales.entries())
          expect(
            locale[match[1]] ?? fallbackMessages[index ? 'zh-CN' : 'en'][match[1]],
            match[1],
          ).toBeTruthy()
      }
    })
  }
})

it.each([
  '<button>删除</button>',
  '<a @click="remove(row)">操作</a>',
  '<button>{{ msg("projects.revoke") }}</button>',
  '<button data-danger-action="deleteItem">操作</button>',
  '<DropdownMenu><button>Delete</button></DropdownMenu>',
  '<button>退出登录</button>',
  '<button>注销</button>',
  '<button>登出</button>',
  '<button>Sign out</button>',
  '<button>Log out</button>',
  '<button>Logout</button>',
  '<button>Log off</button>',
  '<button>Sign-out</button>',
  '<button>{{ msg("nav.signOut") }}</button>',
  '<button @click="logout">Account</button>',
  '<button @click="signOut()">Account</button>',
  '<a href="/logout">Account</a>',
  '<button data-danger-action="signOut">Account</button>',
  '<DropdownMenu><button>退出登录</button></DropdownMenu>',
])('rejects dangerous controls outside the shared component: %s', (source) => {
  expect(checkDangerTemplate(source)).toContain('danger-use-shared-actions')
})
it('rejects custom secondary menus', () => {
  expect(checkDangerTemplate('<ul role="menu"><li>Action</li></ul>')).toContain(
    'secondary-menu-requires-dropdown',
  )
})
it('checks style, dropdown and confirmation in the shared implementation', () => {
  const errors = checkDangerTemplate(
    '<button data-danger-action="deleteItem">Delete</button>',
    'web/ui/src/DangerActions.vue',
  )
  expect(errors).toContain('danger-requires-dropdown')
  expect(errors).toContain('danger-requires-style')
  expect(errors).toContain('danger-requires-confirmation')
})
it('accepts registered sign-out actions with shared confirmation and styling', () => {
  expect(
    checkDangerTemplate(
      '<DropdownMenu><button class="btn-danger" data-danger-action="signOut" @click="action.request()">Sign out</button></DropdownMenu>',
      'web/ui/src/DangerActions.vue',
    ),
  ).toEqual([])
})

describe('icon button label lengths', () => {
  const catalogs = {
    en: { short: '1234567890', long: '12345678901' },
    'zh-CN': { short: '一二三四', long: '一二三四五' },
  }
  it.each(['一二三四', '1234567890', 'Light mode', ''])('accepts boundary text %s', (label) => {
    expect(checkIconLabels(`<button><i />${label}</button>`, '', catalogs)).toEqual([])
  })
  it.each(['一二三四五', '12345678901', '中abcd', 'Save changes'])(
    'rejects long text %s',
    (label) => {
      expect(
        checkIconLabels(`<button><i />${label}</button>`, '', catalogs).some((error) =>
          error.startsWith('icon-button-label-too-long'),
        ),
      ).toBe(true)
    },
  )
  it('checks all translations and computed branches', () => {
    const script = "const label = computed(() => msg(dark ? 'short' : 'long'))"
    expect(checkIconLabels('<button><i />{{ label }}</button>', script, catalogs)).toHaveLength(2)
    expect(checkIconLabels('<button><i />{{ msg("short") }}</button>', '', catalogs)).toEqual([])
  })
  it('counts combined nested text, whitespace and punctuation', () => {
    expect(
      checkIconLabels('<button><i /><span>Save</span> changes!</button>', '', catalogs),
    ).toHaveLength(2)
  })
  it('checks explicit icon buttons and rejects unresolved text', () => {
    expect(checkIconLabels('<IconButton label="12345678901" />', '', catalogs)).toHaveLength(2)
    expect(checkIconLabels('<Button icon="add" :label="unknown" />', '', catalogs)).toContain(
      'icon-button-label-unresolved:en',
    )
    expect(
      checkIconLabels('<button data-icon-button>{{ unknown }}</button>', '', catalogs),
    ).toContain('icon-button-label-unresolved:zh-CN')
  })
  it('allows icon-only accessibility labels and ordinary text buttons', () => {
    expect(
      checkIconLabels(
        '<button aria-label="A long accessible description"><i /></button>',
        '',
        catalogs,
      ),
    ).toEqual([])
    expect(checkIconLabels('<button>A long ordinary text button</button>', '', catalogs)).toEqual(
      [],
    )
  })
})

it('allows sign-out directly in the account menu with confirmation and shared styling', () => {
  const button =
    '<button class="btn-danger" data-danger-action="signOut" @click="accountAction.request()">Sign out</button>'
  expect(
    checkDangerTemplate(`<DropdownMenu>${button}</DropdownMenu>`, 'web/ui/src/PageHeader.vue'),
  ).toEqual([])
  expect(checkDangerTemplate(button, 'web/ui/src/PageHeader.vue')).toContain(
    'danger-requires-dropdown',
  )
  expect(
    checkDangerTemplate(
      `<DropdownMenu>${button.replace('accountAction.request()', 'logout()')}</DropdownMenu>`,
      'web/ui/src/PageHeader.vue',
    ),
  ).toContain('danger-requires-confirmation')
  expect(
    checkDangerTemplate(
      `<DropdownMenu>${button.replace('btn-danger', 'btn')}</DropdownMenu>`,
      'web/ui/src/PageHeader.vue',
    ),
  ).toContain('danger-requires-style')
  expect(
    checkDangerTemplate(
      `<DropdownMenu>${button.replace('signOut', 'deleteUser')}</DropdownMenu>`,
      'web/ui/src/PageHeader.vue',
    ),
  ).toContain('danger-use-shared-actions')
})
