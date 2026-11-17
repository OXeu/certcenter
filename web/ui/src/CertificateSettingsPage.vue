<script setup lang="ts">
import { computed, onMounted } from 'vue'
import DialogPanel from './DialogPanel.vue'
import { createSettingsWorkspace } from './workspace.ts'
import { settingsSections } from './settings.ts'
import type { PageContext } from './page.ts'
const props = defineProps<{ page: PageContext }>()
const { msg } = props.page
const { state, form, refresh, edit, rows } = createSettingsWorkspace(props.page)
const unavailable = computed(() => state.busy || !state.settings)
const activation = computed(() =>
  msg(state.settings?.restart_required ? 'settings.restartRequired' : 'settings.active'),
)
const encryption = computed(() =>
  msg(state.settings?.encryption_configured ? 'settings.configured' : 'settings.notConfigured'),
)
onMounted(refresh)
</script>
<template>
  <div class="my-4 flex flex-wrap items-center gap-3">
    <button class="btn-outline" :disabled="state.busy" @click="refresh">
      {{ msg('common.refresh') }}
    </button>
    <span v-if="state.settings" class="badge">{{ activation }}</span>
  </div>
  <p class="mb-4 text-sm text-muted-foreground">{{ msg('settings.help') }}</p>
  <div v-if="state.settings" class="space-y-4">
    <section
      v-for="section in settingsSections"
      :key="section"
      class="card space-y-3"
      :aria-label="msg(`settings.${section}`)"
    >
      <div class="flex items-center justify-between gap-3">
        <h2 class="font-medium">{{ msg(`settings.${section}`) }}</h2>
        <button class="btn-outline" :disabled="unavailable" @click="edit(section)">
          {{ msg('common.edit') }}
        </button>
      </div>
      <p class="text-sm text-muted-foreground">{{ msg(`settings.${section}Help`) }}</p>
      <dl class="grid gap-x-4 gap-y-2 sm:grid-cols-2">
        <template v-for="row in rows(section)" :key="row.label">
          <dt>{{ msg(row.label) }}</dt>
          <dd class="whitespace-pre-wrap break-all text-sm text-muted-foreground">
            {{ row.value }}
          </dd>
        </template>
      </dl>
    </section>
    <section class="card space-y-2" :aria-label="msg('settings.encryption')">
      <h2 class="font-medium">{{ msg('settings.encryption') }}: {{ encryption }}</h2>
      <p class="text-sm text-muted-foreground">{{ msg('settings.encryptionHelp') }}</p>
    </section>
  </div>
  <DialogPanel v-if="form.open" :form="form" :msg="msg" />
</template>
