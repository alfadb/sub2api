<template>
  <BaseDialog
    :show="show"
    :title="t('admin.accounts.deviceAuth.title')"
    width="normal"
    :close-on-click-outside="false"
    :close-on-escape="!loading"
    @close="handleClose"
  >
    <div class="space-y-6">
      <!-- Loading State -->
      <div v-if="loading && !deviceCodeData" class="flex flex-col items-center justify-center py-8">
        <Icon name="refresh" class="h-10 w-10 animate-spin text-primary-500" />
        <p class="mt-4 text-sm text-gray-500 dark:text-gray-400">
          {{ t('common.processing') }}
        </p>
      </div>

      <!-- Device Code Display -->
      <div v-else-if="deviceCodeData" class="space-y-6">
        <div class="rounded-lg bg-blue-50 p-4 dark:bg-blue-900/20">
          <div class="flex">
            <div class="shrink-0">
              <Icon name="infoCircle" class="h-5 w-5 text-blue-400" aria-hidden="true" />
            </div>
            <div class="ml-3 flex-1 md:flex md:justify-between">
              <p class="text-sm text-blue-700 dark:text-blue-300">
                {{ t('admin.accounts.deviceAuth.step1') }}
              </p>
            </div>
          </div>
          <div class="mt-4 flex items-center justify-between rounded-md bg-white p-3 shadow-sm dark:bg-dark-800">
            <code class="text-xl font-bold tracking-wider text-gray-900 dark:text-white">
              {{ deviceCodeData.user_code }}
            </code>
            <button
              type="button"
              class="ml-4 inline-flex items-center gap-1.5 rounded-md px-2.5 py-1.5 text-sm font-medium text-primary-600 hover:bg-primary-50 dark:text-primary-400 dark:hover:bg-primary-900/20"
              @click="copyCode"
            >
              <Icon name="copy" class="h-4 w-4" />
              {{ copied ? t('common.copied') : t('common.copy') }}
            </button>
          </div>
        </div>

        <div class="text-center">
          <p class="mb-4 text-sm text-gray-500 dark:text-gray-400">
            {{ t('admin.accounts.deviceAuth.step2') }}
          </p>
          <a
            :href="deviceCodeData.verification_uri_complete || deviceCodeData.verification_uri"
            target="_blank"
            rel="noopener noreferrer"
            class="btn btn-primary inline-flex items-center gap-2"
          >
            {{ t('admin.accounts.deviceAuth.openLink') }}
            <Icon name="externalLink" class="h-4 w-4" />
          </a>
        </div>

        <div class="flex items-center justify-center gap-2 text-sm text-gray-500 dark:text-gray-400">
          <Icon name="refresh" class="h-4 w-4 animate-spin" />
          {{ t('admin.accounts.deviceAuth.waiting') }}
        </div>
      </div>

      <!-- Error State -->
      <div v-else-if="error" class="rounded-lg bg-red-50 p-4 dark:bg-red-900/20">
        <div class="flex">
          <div class="shrink-0">
            <Icon name="xCircle" class="h-5 w-5 text-red-400" aria-hidden="true" />
          </div>
          <div class="ml-3">
            <h3 class="text-sm font-medium text-red-800 dark:text-red-200">
              {{ t('common.error') }}
            </h3>
            <div class="mt-2 text-sm text-red-700 dark:text-red-300">
              <p>{{ error }}</p>
            </div>
          </div>
        </div>
      </div>
    </div>

    <template #footer>
      <div class="flex justify-end gap-3">
        <button
          type="button"
          class="btn btn-secondary"
          @click="handleClose"
        >
          {{ t('common.cancel') }}
        </button>
        <button
          v-if="error"
          type="button"
          class="btn btn-primary"
          @click="startAuth"
        >
          {{ t('errors.tryAgain') }}
        </button>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { ref, watch, onUnmounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { useClipboard } from '@/composables/useClipboard'
import { adminAPI } from '@/api/admin'
import type { Account } from '@/types'
import type { GitHubDeviceAuthPollResult, GitHubDeviceAuthStartResult } from '@/api/admin/accounts'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Icon from '@/components/icons/Icon.vue'

const props = defineProps<{
  show: boolean
  account: Account | null
}>()

const emit = defineEmits<{
  (e: 'close'): void
  (e: 'success', account: Account): void
}>()

const { t } = useI18n()
const appStore = useAppStore()
const { copyToClipboard } = useClipboard()

const loading = ref(false)
const error = ref<string | null>(null)
const deviceCodeData = ref<GitHubDeviceAuthStartResult | null>(null)
const copied = ref(false)
const pollingTimer = ref<number | null>(null)
const runId = ref(0)

const nextRun = () => {
  runId.value += 1
  return runId.value
}

const isAccountResult = (value: GitHubDeviceAuthPollResult | Account): value is Account => {
  const v = value as { id?: unknown }
  return typeof v.id === 'number'
}

const copyCode = async () => {
  if (deviceCodeData.value?.user_code) {
    const ok = await copyToClipboard(deviceCodeData.value.user_code)
    if (!ok) return
    copied.value = true
    setTimeout(() => {
      copied.value = false
    }, 2000)
  }
}

const stopPolling = () => {
  if (pollingTimer.value !== null) {
    clearTimeout(pollingTimer.value)
    pollingTimer.value = null
  }
}

const poll = async (run: number) => {
  if (runId.value !== run || !props.show || !props.account || !deviceCodeData.value) return

  try {
    const accountId = props.account.id
    const sessionId = deviceCodeData.value.session_id
    const result = await adminAPI.accounts.pollGitHubDeviceAuth(
      accountId,
      sessionId
    )

    if (runId.value !== run || !props.show) return

    if (isAccountResult(result)) {
      appStore.showSuccess(t('admin.accounts.deviceAuth.success'))
      emit('success', result)
      handleClose()
      return
    }

    const pollResult = result

    if (pollResult.status === 'success') {
      const updatedAccount = await adminAPI.accounts.getById(props.account.id)
      appStore.showSuccess(t('admin.accounts.deviceAuth.success'))
      emit('success', updatedAccount)
      handleClose()
    } else if (pollResult.status === 'error') {
      error.value = pollResult.error_description || pollResult.error || t('admin.accounts.deviceAuth.failed')
      deviceCodeData.value = null
    } else {
      const intervalSeconds = Math.max(1, pollResult.interval ?? deviceCodeData.value.interval ?? 5)
      pollingTimer.value = window.setTimeout(() => poll(run), intervalSeconds * 1000)
    }
  } catch (err: unknown) {
    const e = err as { message?: unknown }
    error.value = typeof e?.message === 'string' ? e.message : t('common.unknownError')
    deviceCodeData.value = null
  }
}

const startAuth = async () => {
  if (!props.account) return

  const run = nextRun()

  loading.value = true
  error.value = null
  deviceCodeData.value = null
  copied.value = false
  stopPolling()

  try {
    const result = await adminAPI.accounts.startGitHubDeviceAuth(props.account.id)
    if (runId.value !== run || !props.show) return
    deviceCodeData.value = result

    // Start polling
    const intervalSeconds = Math.max(1, result.interval ?? 5)
    pollingTimer.value = window.setTimeout(() => poll(run), intervalSeconds * 1000)
  } catch (err: unknown) {
    const e = err as { message?: unknown }
    error.value = typeof e?.message === 'string' ? e.message : t('common.unknownError')
  } finally {
    if (runId.value === run) {
      loading.value = false
    }
  }
}

const handleClose = async () => {
  stopPolling()

  const accountId = props.account?.id
  const sessionId = deviceCodeData.value?.session_id

  // Invalidate any in-flight start/poll and reset local state.
  nextRun()
  loading.value = false
  copied.value = false
  deviceCodeData.value = null
  error.value = null

  // Try to cancel if we have a session
  if (accountId && sessionId) {
    try {
      await adminAPI.accounts.cancelGitHubDeviceAuth(accountId, sessionId)
    } catch (err: unknown) {
      void err
    }
  }
  emit('close')
}

watch(() => props.show, (newVal) => {
  if (newVal) {
    startAuth()
  } else {
    stopPolling()
    nextRun()
    loading.value = false
    deviceCodeData.value = null
    error.value = null
    copied.value = false
  }
})

onUnmounted(() => {
  stopPolling()
  nextRun()
})
</script>
