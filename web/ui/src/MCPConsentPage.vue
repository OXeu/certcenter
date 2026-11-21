<script setup lang="ts">
import ConsentCard from '@oxeu/center-common/ui/ConsentCard.vue'
import { navigateDocument } from '@oxeu/center-common/ui/navigation.ts'
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
const requestTitle = computed(() =>
  consent.value ? msg('mcp.request', { Client: consent.value.client }) : '',
)
const details = computed(() => {
  if (!consent.value) return []
  return [
    { label: msg('mcp.resource'), value: consent.value.resource },
    { label: msg('mcp.redirect'), value: consent.value.redirectUri },
  ]
})

async function decide(approve: boolean) {
  if (busy.value || !consent.value) return
  busy.value = true
  try {
    const result = (await api('/oauth/authorize', {
      method: 'POST',
      csrf: props.page.csrf,
      body: { request: consent.value.request, approve },
    })) as { redirect: string }
    navigateDocument(result.redirect, 'oauth-consent')
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
  <ConsentCard
    v-if="consent"
    :title="requestTitle"
    :description="msg('mcp.permissions')"
    :details="details"
    :allow-label="msg('mcp.authorize')"
    :deny-label="msg('mcp.deny')"
    :busy="busy"
    @allow="authorize"
    @deny="deny"
  />
  <DialogPanel v-if="form.open" :form="form" :msg="msg" />
</template>
