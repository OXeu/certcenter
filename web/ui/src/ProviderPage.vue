<script setup lang="ts">
import Button from '@oxeu/center-common/ui/Button.vue'
import { computed, onMounted } from 'vue'
import DialogPanel from './DialogPanel.vue'
import DangerActions from './DangerActions.vue'
import { createProviderWorkspace } from './workspace.ts'
import type { PageData } from './data.ts'
import type { PageContext } from './page.ts'
const props = defineProps<{ data: PageData; page: PageContext }>()
const { msg } = props.page
const { state, form, refresh, edit, actionsFor, editable } = createProviderWorkspace(
  props.data,
  props.page,
)
const unavailable = computed(() => state.busy || !state.version)
const isDNS = computed(() => props.data.page === 'dns-providers')
const title = computed(() => msg(isDNS.value ? 'cert.dnsProviders' : 'cert.upstreams'))
const addLabel = computed(() => msg(isDNS.value ? 'providers.addDNS' : 'providers.addUpstream'))
onMounted(refresh)
</script>
<template>
  <div class="my-4 flex flex-wrap gap-2">
    <Button :disabled="state.busy" @click="refresh" variant="secondary">
      {{ msg('common.refresh') }}
    </Button>
    <Button :disabled="unavailable" @click="edit()" variant="primary">{{ addLabel }}</Button>
  </div>
  <p class="mb-4 text-sm text-muted-foreground">{{ msg('providers.help') }}</p>
  <p v-if="isDNS" class="mb-4 text-sm text-muted-foreground">{{ msg('providers.managedHelp') }}</p>
  <div class="table-scroll">
    <table class="table" :aria-label="title" :aria-busy="state.busy">
      <thead>
        <tr>
          <th>{{ msg('providers.name') }}</th>
          <th>{{ msg('providers.kind') }}</th>
          <th>{{ msg('common.actions') }}</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="provider in state.providers" :key="provider.name">
          <td>{{ provider.name }}</td>
          <td>{{ msg(`providers.${provider.fields.kind}`) }}</td>
          <td>
            <Button
              v-if="editable(provider)"
              :disabled="state.busy"
              @click="edit(provider)"
              variant="ghost"
            >
              {{ msg('common.edit') }}
            </Button>
            <DangerActions
              :actions="actionsFor(provider)"
              :label="msg('common.more')"
              :disabled="state.busy"
            />
          </td>
        </tr>
        <tr v-if="!state.providers.length">
          <td colspan="3">{{ msg('providers.empty') }}</td>
        </tr>
      </tbody>
    </table>
  </div>
  <DialogPanel v-if="form.open" :form="form" :msg="msg" />
</template>
