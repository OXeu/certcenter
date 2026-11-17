<script setup lang="ts">
import { computed, ref } from 'vue'
import { api } from './core.ts'
import { createForm } from './forms.ts'
import DialogPanel from './DialogPanel.vue'
import type { PageData } from './data.ts'
import type { PageContext } from './page.ts'

const props = defineProps<{ data: PageData; page: PageContext }>()
const { msg } = props.page
const consent = computed(() => props.data.consent)
const form = createForm(props.page)
const busy = ref(false)

async function decide(approve: boolean) {
  if (busy.value || !consent.value) return
  busy.value = true
  try {
    const result = (await api('/oauth/authorize', {
      method: 'POST',
      csrf: props.page.csrf,
      body: { request: consent.value.request, approve },
    })) as { redirect: string }
    const target = new URL(result.redirect)
    const expected = new URL(consent.value.redirectUri)
    if (target.origin !== expected.origin || target.pathname !== expected.pathname)
      throw new Error(msg('error.generic'))
    location.assign(target.href)
  } catch (err) {
    busy.value = false
    throw err
  }
}
function authorize() {
  if (busy.value) return
  form.show(msg('mcp.authorize'), [], {}, () => decide(true))
}
async function deny() {
  try {
    await decide(false)
  } catch (err) {
    props.page.report(err)
  }
}
</script>
<template>
  <section v-if="consent" class="card mt-4 space-y-4">
    <p>{{ msg('mcp.request', { Client: consent.client }) }}</p>
    <p class="text-sm text-muted-foreground">{{ msg('mcp.permissions') }}</p>
    <dl class="space-y-2 break-all text-sm">
      <dt class="font-medium">{{ msg('mcp.resource') }}</dt>
      <dd>{{ consent.resource }}</dd>
      <dt class="font-medium">{{ msg('mcp.redirect') }}</dt>
      <dd>{{ consent.redirectUri }}</dd>
    </dl>
    <div class="flex gap-2">
      <button class="btn" type="button" :disabled="busy" @click="authorize">
        {{ msg('mcp.authorize') }}
      </button>
      <button class="btn-outline" type="button" :disabled="busy" @click="deny">
        {{ msg('mcp.deny') }}
      </button>
    </div>
  </section>
  <DialogPanel v-if="form.open" :form="form" :msg="msg" />
</template>
