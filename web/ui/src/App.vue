<script setup>
import { computed, defineAsyncComponent, onMounted, watch } from 'vue'
import PageHeader from './PageHeader.vue'
import ToastViewport from './ToastViewport.vue'
import { createPage } from './page.ts'
import { useTheme } from './theme.ts'
import { mountToasts, notify, setToastTheme } from './toast.ts'

const MCPConsentPage = defineAsyncComponent(() => import('./MCPConsentPage.vue'))
const WorkspacePage = defineAsyncComponent(() => import('./WorkspacePage.vue'))
const CertificatePage = defineAsyncComponent(() => import('./CertificatePage.vue'))
const CertificateSettingsPage = defineAsyncComponent(() => import('./CertificateSettingsPage.vue'))
const ProviderPage = defineAsyncComponent(() => import('./ProviderPage.vue'))

const props = defineProps({ data: { type: Object, required: true } })
const data = /** @type {import('./data.ts').PageData} */ (props.data)
const { theme, toggleTheme } = useTheme()
watch(theme, (value) => setToastTheme(value))
const isCertificatePage = computed(() => ['domains', 'certificates'].includes(data.page))
const isSettingsPage = computed(() => data.page === 'certificate-settings')
const isProviderPage = computed(() => ['dns-providers', 'upstreams'].includes(data.page))
const isWorkspace = computed(() =>
  ['projects', 'project', 'users', 'settings', 'keys'].includes(data.page),
)
const page = createPage(data)
const { msg } = page
const title = computed(
  () => data.project?.name ?? msg(data.page === 'login' ? 'login.title' : data.title),
)
onMounted(() => {
  mountToasts(data.lang, data.nonce, theme.value)
  if (data.flash) notify(data.flash.level, data.flash.message)
  if (data.page === 'login' && !data.oauthEnabled) notify('info', msg('login.notConfigured'))
})
</script>
<template>
  <div :data-theme="theme" class="min-h-screen bg-background text-foreground">
    <ToastViewport />
    <PageHeader
      :data="data"
      :msg="msg"
      :title="title"
      :theme="theme"
      subtitle=""
      @toggle-theme="toggleTheme"
    >
      <a v-if="data.page === 'login' && data.oauthEnabled" class="btn mt-4" href="/auth/login">{{
        msg('login.continue', { ProviderName: data.providerName })
      }}</a>
      <MCPConsentPage v-if="data.page === 'mcp-consent'" :data="data" :page="page" />
      <WorkspacePage v-if="isWorkspace" :data="data" :page="page" :title="title" />
      <CertificatePage v-if="isCertificatePage" :data="data" :page="page" />
      <CertificateSettingsPage v-if="isSettingsPage" :page="page" />
      <ProviderPage v-if="isProviderPage" :data="data" :page="page" />
    </PageHeader>
  </div>
</template>
