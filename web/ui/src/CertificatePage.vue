<script setup lang="ts">
import { computed, onMounted, onUnmounted } from 'vue'
import DialogPanel from './DialogPanel.vue'
import DangerActions from './DangerActions.vue'
import { createCertificateWorkspace } from './workspace.ts'
import type { PageData } from './data.ts'
import type { PageContext } from './page.ts'

const props = defineProps<{ data: PageData; page: PageContext }>()
const { msg } = props.page
const { state, form, refresh, createDomain, editPolicy, actionsFor, pending, validity, status } =
  createCertificateWorkspace(props.data, props.page)
const isDomains = computed(() => props.data.page === 'domains')
const isSettings = computed(() => props.data.page === 'certificate-settings')
const policies = computed(() => state.detail?.domain.certificates ?? [])
const tabs = ['policies', 'history', 'logs']
const canCreate = computed(() => isDomains.value && props.data.isAdmin)
const settingsHelp = computed(() =>
  state.settings?.local ? 'cert.localSettingsHelp' : 'cert.settingsHelp',
)
const settingsURL = computed(() => {
  const url = state.settings?.configcenter_url ?? ''
  return /^https?:\/\//.test(url) ? url : '#'
})
function domainURL(name: string) {
  return `/domains/${encodeURIComponent(name)}`
}
function date(value: string) {
  return new Date(value).toLocaleString(props.data.lang)
}
function yes(value: boolean) {
  return msg(value ? 'cert.yes' : 'cert.no')
}
let timer: ReturnType<typeof setInterval> | undefined
onMounted(() => {
  void refresh()
  timer = setInterval(() => {
    void refresh()
  }, 10000)
})
onUnmounted(() => {
  if (timer) clearInterval(timer)
})
</script>

<template>
  <div class="my-4 flex flex-wrap gap-2">
    <button class="btn-outline" :disabled="state.busy" @click="refresh">
      {{ msg('common.refresh') }}
    </button>
    <button v-if="canCreate" class="btn" :disabled="state.busy" @click="createDomain">
      {{ msg('cert.addDomain') }}
    </button>
    <button v-if="state.detail" class="btn" :disabled="state.busy" @click="editPolicy()">
      {{ msg('cert.addCertificate') }}
    </button>
  </div>

  <div v-if="isDomains" class="table-scroll">
    <table class="table" :aria-label="msg('cert.domains')" :aria-busy="state.busy">
      <thead>
        <tr>
          <th>{{ msg('cert.domain') }}</th>
          <th>{{ msg('cert.owner') }}</th>
          <th>{{ msg('cert.certificates') }}</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="domain in state.domains" :key="domain.name">
          <td><a :href="domainURL(domain.name)">{{ domain.name }}</a></td>
          <td>{{ domain.owner }}</td>
          <td>{{ domain.count }}</td>
        </tr>
        <tr v-if="!state.domains.length">
          <td colspan="3">{{ msg('cert.emptyDomains') }}</td>
        </tr>
      </tbody>
    </table>
  </div>

  <section v-if="isSettings && state.settings" class="card space-y-3">
    <p>{{ msg(settingsHelp) }}</p>
    <a v-if="!state.settings.local" class="btn-outline" :href="settingsURL">{{
      msg('cert.openConfigCenter')
    }}</a>
    <dl class="space-y-2">
      <dt class="font-medium">{{ msg('cert.authority') }}</dt>
      <dd class="break-all">{{ state.settings.directory_url }}</dd>
      <dt class="font-medium">{{ msg('users.email') }}</dt>
      <dd>{{ state.settings.email }}</dd>
      <dt class="font-medium">{{ msg('cert.terms') }}</dt>
      <dd>{{ yes(state.settings.accept_terms) }}</dd>
      <dt class="font-medium">{{ msg('cert.dnsProvider') }}</dt>
      <dd>{{ state.settings.dns_providers.join(', ') }}</dd>
      <dt class="font-medium">{{ msg('cert.upstreams') }}</dt>
      <dd>{{ state.settings.upstreams.join(', ') }}</dd>
    </dl>
  </section>

  <template v-if="state.detail">
    <nav class="mb-4 flex gap-2" :aria-label="msg('cert.certificates')">
      <button
        v-for="tab in tabs"
        :key="tab"
        class="tabs-trigger"
        :data-active="state.tab === tab"
        @click="state.tab = tab"
      >
        {{ msg(`cert.${tab}`) }}
      </button>
    </nav>
    <div v-if="state.tab === 'policies'" class="space-y-4">
      <p class="text-sm text-muted-foreground">{{ msg('cert.policyHelp') }}</p>
      <p class="text-sm text-muted-foreground">
        {{ msg('cert.upstreams') }}: {{ state.detail.upstreams.join(', ') }}
      </p>
      <div class="table-scroll">
        <table class="table" :aria-label="msg('cert.policies')">
          <thead>
            <tr>
              <th>{{ msg('cert.policy') }}</th>
              <th>{{ msg('cert.names') }}</th>
              <th>{{ msg('cert.expires') }}</th>
              <th>{{ msg('cert.automation') }}</th>
              <th>{{ msg('common.actions') }}</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="policy in policies" :key="policy.id">
              <td>
                <span class="font-medium">{{ policy.id }}</span>
                <p>{{ status(policy) }}</p>
                <span v-if="!policy.enabled" class="badge">{{ msg('cert.disabled') }}</span>
              </td>
              <td class="break-all">
                {{ policy.names.join(', ') }}
                <p class="text-muted-foreground">{{ policy.dns_provider }}</p>
              </td>
              <td>{{ validity(policy) }}</td>
              <td>
                {{ msg('cert.autoRenew') }}: {{ yes(policy.auto_renew) }}
                <p>{{ msg('cert.autoDeploy') }}: {{ yes(policy.auto_deploy) }}</p>
              </td>
              <td>
                <button class="btn-ghost" :disabled="state.busy" @click="editPolicy(policy)">
                  {{ msg('common.edit') }}
                </button>
                <DangerActions
                  :actions="actionsFor(policy)"
                  :label="msg('common.more')"
                  :disabled="pending(policy)"
                />
              </td>
            </tr>
            <tr v-if="!policies.length">
              <td colspan="5">{{ msg('cert.emptyCertificates') }}</td>
            </tr>
          </tbody>
        </table>
      </div>
      <section v-if="state.detail.delegations.length" class="card">
        <h2 class="font-medium">{{ msg('cert.delegation') }}</h2>
        <p class="text-sm text-muted-foreground">{{ msg('cert.delegationHelp') }}</p>
        <p
          v-for="record in state.detail.delegations"
          :key="record.name + record.target"
          class="break-all font-mono text-sm"
        >
          {{ record.name }} CNAME {{ record.target }}
        </p>
      </section>
    </div>

    <div v-if="state.tab === 'history'" class="table-scroll">
      <table class="table" :aria-label="msg('cert.history')">
        <thead>
          <tr>
            <th>{{ msg('cert.policy') }}</th>
            <th>{{ msg('cert.names') }}</th>
            <th>{{ msg('cert.validFrom') }}</th>
            <th>{{ msg('cert.expires') }}</th>
            <th>{{ msg('cert.fingerprint') }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="certificate in state.detail.certificates" :key="certificate.id">
            <td>{{ certificate.policy_id }}</td>
            <td>{{ certificate.names.join(', ') }}</td>
            <td>{{ date(certificate.not_before) }}</td>
            <td>{{ date(certificate.not_after) }}</td>
            <td class="break-all font-mono text-xs">{{ certificate.fingerprint }}</td>
          </tr>
        </tbody>
      </table>
    </div>

    <div v-if="state.tab === 'logs'" class="space-y-4">
      <div class="table-scroll">
        <table class="table" :aria-label="msg('cert.jobs')">
          <thead>
            <tr>
              <th>{{ msg('cert.job') }}</th>
              <th>{{ msg('cert.policy') }}</th>
              <th>{{ msg('common.actions') }}</th>
              <th>{{ msg('cert.status') }}</th>
              <th>{{ msg('cert.time') }}</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="job in state.detail.jobs" :key="job.id">
              <td>{{ job.id }}</td>
              <td>{{ job.policy_id }}</td>
              <td>{{ msg(`cert.${job.action}`) }}</td>
              <td>{{ msg(`cert.${job.status}`) }}</td>
              <td>{{ date(job.created_at) }}</td>
            </tr>
          </tbody>
        </table>
      </div>
      <div class="table-scroll">
        <table class="table" :aria-label="msg('cert.logs')">
          <thead>
            <tr>
              <th>{{ msg('cert.time') }}</th>
              <th>{{ msg('cert.job') }}</th>
              <th>{{ msg('cert.stage') }}</th>
              <th>{{ msg('cert.upstreams') }}</th>
              <th>{{ msg('cert.message') }}</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="event in state.detail.events" :key="event.id">
              <td>{{ date(event.created_at) }}</td>
              <td>{{ event.job_id }}</td>
              <td>{{ msg(`cert.${event.stage}`) }}</td>
              <td>{{ event.target }}</td>
              <td class="break-all">{{ event.message }}</td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>
  </template>
  <DialogPanel v-if="form.open" :form="form" :msg="msg" />
</template>
