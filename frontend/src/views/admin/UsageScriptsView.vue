<template>
  <AppLayout>
    <TablePageLayout>
      <template #filters>
        <div class="flex items-center justify-between">
          <div>
            <h2 class="text-lg font-semibold text-gray-900 dark:text-white">
              {{ t('usageScripts.title') }}
            </h2>
            <p class="text-sm text-gray-500 dark:text-gray-400">
              {{ t('usageScripts.description') }}
            </p>
          </div>
          <button class="btn btn-primary" @click="openCreateDialog">
            {{ t('usageScripts.create') }}
          </button>
        </div>
      </template>

      <template #table>
        <div v-if="loading" class="flex items-center justify-center py-12">
          <LoadingSpinner />
        </div>

        <div v-else-if="scripts.length === 0" class="py-12 text-center">
          <p class="text-gray-500 dark:text-gray-400">{{ t('usageScripts.noScripts') }}</p>
          <button class="btn btn-primary mt-4" @click="openCreateDialog">
            {{ t('usageScripts.createFirst') }}
          </button>
        </div>

        <table v-else class="w-full text-left text-sm">
          <thead class="border-b border-gray-200 text-xs uppercase text-gray-500 dark:border-gray-700 dark:text-gray-400">
            <tr>
              <th class="px-4 py-3">{{ t('usageScripts.columns.baseUrlHost') }}</th>
              <th class="px-4 py-3">{{ t('usageScripts.columns.accountType') }}</th>
              <th class="px-4 py-3">{{ t('usageScripts.columns.enabled') }}</th>
              <th class="px-4 py-3">{{ t('usageScripts.columns.createdAt') }}</th>
              <th class="px-4 py-3 text-right">{{ t('usageScripts.columns.actions') }}</th>
            </tr>
          </thead>
          <tbody class="divide-y divide-gray-200 dark:divide-gray-700">
            <tr
              v-for="script in scripts"
              :key="script.id"
              class="hover:bg-gray-50 dark:hover:bg-gray-800/50"
            >
              <td class="px-4 py-3 font-mono text-xs">{{ script.base_url_host }}</td>
              <td class="px-4 py-3">
                <span class="badge badge-gray">
                  {{ t(`usageScripts.accountTypes.${script.account_type}`) }}
                </span>
              </td>
              <td class="px-4 py-3">
                <button
                  class="relative inline-flex h-5 w-9 items-center rounded-full transition-colors"
                  :class="script.enabled ? 'bg-green-500' : 'bg-gray-300 dark:bg-gray-600'"
                  @click="toggleEnabled(script)"
                >
                  <span
                    class="inline-block h-3.5 w-3.5 transform rounded-full bg-white transition-transform"
                    :class="script.enabled ? 'translate-x-4' : 'translate-x-0.5'"
                  />
                </button>
              </td>
              <td class="px-4 py-3 text-xs text-gray-500">
                {{ formatDate(script.created_at) }}
              </td>
              <td class="px-4 py-3 text-right">
                <button
                  class="text-sm text-blue-600 hover:text-blue-800 dark:text-blue-400 dark:hover:text-blue-300"
                  @click="openEditDialog(script)"
                >
                  {{ t('usageScripts.edit') }}
                </button>
                <button
                  class="ml-3 text-sm text-red-600 hover:text-red-800 dark:text-red-400 dark:hover:text-red-300"
                  @click="confirmDelete(script)"
                >
                  {{ t('common.delete') }}
                </button>
              </td>
            </tr>
          </tbody>
        </table>
      </template>
    </TablePageLayout>

    <!-- Create/Edit Dialog -->
    <div
      v-if="showDialog"
      class="fixed inset-0 z-50 flex items-center justify-center bg-black/50"
      @click.self="closeDialog"
    >
      <div class="card w-full max-w-2xl mx-4 max-h-[90vh] overflow-y-auto">
        <div class="p-6">
          <h3 class="text-lg font-semibold text-gray-900 dark:text-white mb-4">
            {{ editingScript ? t('usageScripts.edit') : t('usageScripts.create') }}
          </h3>

          <form @submit.prevent="saveScript" class="space-y-4">
            <div>
              <label class="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1">
                {{ t('usageScripts.form.baseUrlHost') }}
              </label>
              <input
                v-model="form.base_url_host"
                type="text"
                class="input w-full"
                :placeholder="t('usageScripts.form.baseUrlHostPlaceholder')"
                required
              />
              <p class="mt-1 text-xs text-gray-500">{{ t('usageScripts.form.baseUrlHostHint') }}</p>
            </div>

            <div>
              <label class="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1">
                {{ t('usageScripts.form.accountType') }}
              </label>
              <select v-model="form.account_type" class="input w-full" required>
                <option value="" disabled>{{ t('usageScripts.form.accountTypePlaceholder') }}</option>
                <option value="oauth">{{ t('usageScripts.accountTypes.oauth') }}</option>
                <option value="apikey">{{ t('usageScripts.accountTypes.apikey') }}</option>
                <option value="setup-token">{{ t('usageScripts.accountTypes.setup-token') }}</option>
              </select>
            </div>

            <div>
              <label class="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1">
                {{ t('usageScripts.form.script') }}
              </label>
              <textarea
                v-model="form.script"
                class="input w-full font-mono text-xs"
                rows="12"
                :placeholder="t('usageScripts.form.scriptPlaceholder')"
                required
              />
            </div>

            <div class="flex items-center gap-2">
              <input
                v-model="form.enabled"
                type="checkbox"
                id="script-enabled"
                class="h-4 w-4 rounded border-gray-300"
              />
              <label for="script-enabled" class="text-sm text-gray-700 dark:text-gray-300">
                {{ t('usageScripts.form.enabled') }}
              </label>
            </div>

            <div class="flex justify-end gap-3 pt-2">
              <button type="button" class="btn btn-secondary" @click="closeDialog">
                {{ t('common.cancel') }}
              </button>
              <button type="submit" class="btn btn-primary" :disabled="saving">
                {{ saving ? t('common.saving') : t('common.save') }}
              </button>
            </div>
          </form>
        </div>
      </div>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { usageScriptsAPI, type UsageScript } from '@/api/admin/usageScripts'
import { AppLayout } from '@/components/layout'
import TablePageLayout from '@/components/layout/TablePageLayout.vue'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'

const { t } = useI18n()
const appStore = useAppStore()

const scripts = ref<UsageScript[]>([])
const loading = ref(false)
const saving = ref(false)
const showDialog = ref(false)
const editingScript = ref<UsageScript | null>(null)

const form = ref({
  base_url_host: '',
  account_type: '',
  script: '',
  enabled: true
})

async function loadScripts() {
  loading.value = true
  try {
    scripts.value = await usageScriptsAPI.list()
  } catch {
    appStore.showError(t('usageScripts.messages.failedToLoad'))
  } finally {
    loading.value = false
  }
}

function openCreateDialog() {
  editingScript.value = null
  form.value = { base_url_host: '', account_type: '', script: '', enabled: true }
  showDialog.value = true
}

function openEditDialog(script: UsageScript) {
  editingScript.value = script
  form.value = {
    base_url_host: script.base_url_host,
    account_type: script.account_type,
    script: script.script,
    enabled: script.enabled
  }
  showDialog.value = true
}

function closeDialog() {
  showDialog.value = false
  editingScript.value = null
}

async function saveScript() {
  saving.value = true
  try {
    if (editingScript.value) {
      await usageScriptsAPI.update(editingScript.value.id, form.value)
      appStore.showSuccess(t('usageScripts.messages.updated'))
    } else {
      await usageScriptsAPI.create(form.value)
      appStore.showSuccess(t('usageScripts.messages.created'))
    }
    closeDialog()
    await loadScripts()
  } catch {
    appStore.showError(t('usageScripts.messages.failedToSave'))
  } finally {
    saving.value = false
  }
}

async function toggleEnabled(script: UsageScript) {
  try {
    await usageScriptsAPI.update(script.id, {
      base_url_host: script.base_url_host,
      account_type: script.account_type,
      script: script.script,
      enabled: !script.enabled
    })
    script.enabled = !script.enabled
  } catch {
    appStore.showError(t('usageScripts.messages.failedToSave'))
  }
}

async function confirmDelete(script: UsageScript) {
  if (!confirm(t('usageScripts.messages.deleteConfirm'))) return
  try {
    await usageScriptsAPI.delete(script.id)
    appStore.showSuccess(t('usageScripts.messages.deleted'))
    await loadScripts()
  } catch {
    appStore.showError(t('usageScripts.messages.failedToDelete'))
  }
}

function formatDate(dateStr: string) {
  return new Date(dateStr).toLocaleDateString(undefined, {
    year: 'numeric',
    month: 'short',
    day: 'numeric'
  })
}

onMounted(loadScripts)
</script>
