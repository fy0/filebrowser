<template>
  <!-- 折叠按钮 -->
  <button
    class="seal-toggle-btn"
    :class="{ collapsed: isCollapsed }"
    @click="toggleCollapsed"
    :title="isCollapsed ? t('seal.expand') : t('seal.collapse')"
  >
    <img v-if="isCollapsed" src="/img/seal.png" alt="Seal" class="seal-icon" />
    <i v-else class="material-icons">close</i>
  </button>

  <div id="seal-toolbar" :class="{ hidden: shouldHide, collapsed: isCollapsed }">
    <div class="seal-toolbar-content">
      <span class="seal-toolbar-title">{{ t("seal.title") }}</span>
      <div class="seal-toolbar-actions">
        <button
          @click="confirmImportBackup"
          :title="t('seal.importBackup')"
          class="seal-action"
          :disabled="isImporting"
        >
          <i class="material-icons">{{ isImporting ? 'hourglass_empty' : 'upload_file' }}</i>
          <span>{{ isImporting ? t("seal.importing") : t("seal.importBackup") }}</span>
        </button>
        <button
          @click="downloadLatestBackup"
          :title="t('seal.downloadLatest')"
          class="seal-action"
          :disabled="isDownloading"
        >
          <i class="material-icons">{{ isDownloading ? 'hourglass_empty' : 'cloud_download' }}</i>
          <span>{{ t("seal.downloadLatest") }}</span>
        </button>
        <button
          @click="viewBackups"
          :title="t('seal.viewBackups')"
          class="seal-action"
        >
          <i class="material-icons">folder_open</i>
          <span>{{ t("seal.viewBackups") }}</span>
        </button>
      </div>
    </div>
    <input
      type="file"
      ref="fileInput"
      @change="handleFileSelect"
      accept=".zip"
      style="display: none"
    />
  </div>

  <!-- 上传进度弹窗 -->
  <div v-if="isUploading" class="seal-upload-overlay">
    <div class="seal-upload-dialog">
      <div class="seal-upload-header">
        <i class="material-icons">cloud_upload</i>
        <span>{{ t("seal.uploading") }}</span>
      </div>
      <div class="seal-upload-content">
        <div class="seal-upload-filename">{{ uploadFileName }}</div>
        <div class="seal-upload-progress-bar">
          <div class="seal-upload-progress-fill" :style="{ width: uploadProgress + '%' }"></div>
        </div>
        <div class="seal-upload-stats">
          <span class="seal-upload-percent">{{ uploadProgress.toFixed(1) }}%</span>
          <span class="seal-upload-speed">{{ uploadSpeedText }}</span>
          <span class="seal-upload-size">{{ uploadedSizeText }} / {{ totalSizeText }}</span>
        </div>
      </div>
    </div>
  </div>

  <!-- 导入备份警告弹框 -->
  <div v-if="showImportWarning" class="seal-warning-overlay" @click.self="showImportWarning = false">
    <div class="seal-warning-dialog">
      <div class="seal-warning-header">
        <i class="material-icons warning-icon">warning</i>
        <span>{{ t("seal.importWarningTitle") }}</span>
      </div>
      <div class="seal-warning-content">
        <p>{{ t("seal.importWarningMessage") }}</p>
        <ul>
          <li>{{ t("seal.importWarningItem1") }}</li>
          <li>{{ t("seal.importWarningItem2") }}</li>
          <li>{{ t("seal.importWarningItem3") }}</li>
        </ul>
        <p class="seal-warning-disclaimer">{{ t("seal.importWarningDisclaimer") }}</p>
      </div>
      <div class="seal-warning-actions">
        <button class="seal-warning-btn cancel" @click="showImportWarning = false">
          {{ t("buttons.cancel") }}
        </button>
        <button class="seal-warning-btn confirm" @click="proceedImport">
          {{ t("seal.confirmImport") }}
        </button>
      </div>
    </div>
  </div>

  <!-- 已存在备份文件询问弹框 -->
  <div v-if="showExistingBackupDialog" class="seal-warning-overlay" @click.self="showExistingBackupDialog = false">
    <div class="seal-warning-dialog">
      <div class="seal-warning-header">
        <i class="material-icons" style="color: var(--blue);">info</i>
        <span>{{ t("seal.existingBackupTitle") }}</span>
      </div>
      <div class="seal-warning-content">
        <p>{{ t("seal.existingBackupMessage") }}</p>
        <div class="seal-existing-backup-info">
          <div><strong>{{ t("seal.fileSize") }}:</strong> {{ existingBackupSize }}</div>
          <div><strong>{{ t("seal.fileModified") }}:</strong> {{ existingBackupModified }}</div>
        </div>
      </div>
      <div class="seal-warning-actions three-buttons">
        <button class="seal-warning-btn cancel" @click="showExistingBackupDialog = false">
          {{ t("buttons.cancel") }}
        </button>
        <button class="seal-warning-btn" style="background: var(--blue); color: white;" @click="useExistingBackup">
          {{ t("seal.useExisting") }}
        </button>
        <button class="seal-warning-btn confirm" @click="uploadNewBackup">
          {{ t("seal.uploadNew") }}
        </button>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, inject, onUnmounted } from "vue";
import { useRouter } from "vue-router";
import { useI18n } from "vue-i18n";
import { useFileStore } from "@/stores/file";
import { files as api } from "@/api";
import { fetchURL } from "@/api/utils";
import { baseURL } from "@/utils/constants";
import { useAuthStore } from "@/stores/auth";

const { t } = useI18n();
const router = useRouter();
const fileStore = useFileStore();
const authStore = useAuthStore();
const $showError = inject<IToastError>("$showError")!;
const $showSuccess = inject<IToastSuccess>("$showSuccess")!;

const props = defineProps<{
  isMobile: boolean;
}>();

const fileInput = ref<HTMLInputElement | null>(null);
const isImporting = ref(false);
const isDownloading = ref(false);
const isCollapsed = ref(props.isMobile); // 移动端默认折叠，桌面端默认展开
const showImportWarning = ref(false);

// 备份文件路径
const BACKUP_UPLOAD_PATH = "/sealdice/__bak_upload.zip";

// 已存在备份文件弹窗相关
const showExistingBackupDialog = ref(false);
const existingBackupSize = ref("");
const existingBackupModified = ref("");
const pendingUploadFile = ref<File | null>(null);

// 上传进度相关
const isUploading = ref(false);
const uploadProgress = ref(0);
const uploadSpeed = ref(0);
const uploadedBytes = ref(0);
const totalBytes = ref(0);
const uploadFileName = ref("");
const uploadStartTime = ref(0);
const lastUploadedBytes = ref(0);
const lastSpeedUpdateTime = ref(0);

const formatFileSize = (bytes: number): string => {
  if (bytes < 1024) return bytes + " B";
  if (bytes < 1024 * 1024) return (bytes / 1024).toFixed(2) + " KB";
  return (bytes / (1024 * 1024)).toFixed(2) + " MB";
};

const uploadSpeedText = computed(() => {
  return formatFileSize(uploadSpeed.value) + "/s";
});

const uploadedSizeText = computed(() => {
  return formatFileSize(uploadedBytes.value);
});

const totalSizeText = computed(() => {
  return formatFileSize(totalBytes.value);
});

const shouldHide = computed(() => {
  // 移动端：未选中文件时显示，选中文件时隐藏
  // 桌面端：一直显示
  if (props.isMobile) {
    return fileStore.selectedCount > 0;
  }
  return false;
});

const toggleCollapsed = () => {
  isCollapsed.value = !isCollapsed.value;
};

const confirmImportBackup = () => {
  showImportWarning.value = true;
};

const proceedImport = () => {
  showImportWarning.value = false;
  importBackup();
};

const importBackup = () => {
  if (fileInput.value) {
    fileInput.value.click();
  }
};

const handleFileSelect = async (event: Event) => {
  const input = event.target as HTMLInputElement;
  if (!input.files || input.files.length === 0) return;

  const file = input.files[0];
  if (!file.name.toLowerCase().endsWith(".zip")) {
    $showError(t("seal.invalidFileType"));
    input.value = "";
    return;
  }

  // 保存待上传的文件
  pendingUploadFile.value = file;

  // 检查是否存在已上传的备份文件
  try {
    const response = await fetchURL(`/api/resources${BACKUP_UPLOAD_PATH}`, {});
    if (response.ok) {
      const data = await response.json();
      existingBackupSize.value = formatFileSize(data.size || 0);
      existingBackupModified.value = new Date(data.modified).toLocaleString();
      showExistingBackupDialog.value = true;
      input.value = "";
      return;
    }
  } catch (e) {
    // 文件不存在，继续上传
  }

  // 文件不存在，直接上传
  input.value = "";
  await startUploadAndImport();
};

// 使用已存在的备份文件
const useExistingBackup = async () => {
  showExistingBackupDialog.value = false;
  pendingUploadFile.value = null;
  await performImport();
};

// 上传新的备份文件
const uploadNewBackup = async () => {
  showExistingBackupDialog.value = false;
  await startUploadAndImport();
};

// 开始上传并导入
const startUploadAndImport = async () => {
  if (!pendingUploadFile.value) return;

  isImporting.value = true;

  try {
    // 上传备份文件
    await uploadFile(pendingUploadFile.value, BACKUP_UPLOAD_PATH);
    pendingUploadFile.value = null;

    // 校验 zip 文件完整性
    const isValid = await validateZipFile();
    if (!isValid) {
      $showError(t("seal.invalidZipFile"));
      return;
    }

    // 执行导入
    await performImport();
  } catch (error: any) {
    console.error("Upload error:", error);
    $showError(error);
    isImporting.value = false;
  }
};

// 校验 zip 文件完整性
const validateZipFile = async (): Promise<boolean> => {
  try {
    // 使用 checksum API 来验证文件
    const response = await fetchURL(`/api/checksum${BACKUP_UPLOAD_PATH}?algo=md5`, {});
    if (response.ok) {
      const data = await response.json();
      // 如果能计算出 checksum，说明文件完整
      return !!data.checksums && data.checksums.length > 0;
    }
    return false;
  } catch (e) {
    console.warn("Zip validation failed:", e);
    // 如果没有 checksum API，尝试其他方式验证
    // 简单检查文件是否存在且大小大于0
    try {
      const response = await fetchURL(`/api/resources${BACKUP_UPLOAD_PATH}`, {});
      if (response.ok) {
        const data = await response.json();
        return data.size > 0;
      }
    } catch (e2) {
      // 忽略
    }
    return false;
  }
};

// 执行导入操作
const performImport = async () => {
  isImporting.value = true;

  try {
    // 步骤1: 创建临时解压目录
    const tempExtractPath = "/sealdice/_temp_extract/";
    try {
      await fetchURL(`/api/resources${tempExtractPath}`, { method: "DELETE" });
    } catch (e) {
      // 忽略删除错误，目录可能不存在
    }

    // 步骤2: 解压备份文件到临时目录
    await fetchURL(`/api/extract${BACKUP_UPLOAD_PATH}?destination=${encodeURIComponent(tempExtractPath)}`, {
      method: "POST",
    });

    // 步骤3: 删除旧的 data 目录
    try {
      await fetchURL("/api/resources/sealdice/data", { method: "DELETE" });
    } catch (e) {
      // 忽略删除错误，目录可能不存在
    }

    // 步骤4: 移动解压出的 data 目录到目标位置
    await fetchURL(
      `/api/resources/sealdice/_temp_extract/data?action=rename&destination=${encodeURIComponent("/sealdice/data")}`,
      { method: "PATCH" }
    );

    // 步骤5: 读取并修改 dice.yaml
    await updateDiceYaml();

    // 步骤6: 清理临时文件
    try {
      await fetchURL(`/api/resources${tempExtractPath}`, { method: "DELETE" });
    } catch (e) {
      // 忽略清理错误
    }

    // 步骤7: 导入完成后清理上传的备份文件
    try {
      await fetchURL(`/api/resources${BACKUP_UPLOAD_PATH}`, { method: "DELETE" });
    } catch (e) {
      // 忽略删除错误（例如文件不存在或权限不足）
    }

    $showSuccess(t("seal.importSuccess"));
    fileStore.reload = true;
  } catch (error: any) {
    console.error("Import backup error:", error);
    $showError(error);
  } finally {
    isImporting.value = false;
  }
};

const uploadFile = (file: File, path: string): Promise<void> => {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest();

    // 初始化上传状态
    isUploading.value = true;
    uploadProgress.value = 0;
    uploadSpeed.value = 0;
    uploadedBytes.value = 0;
    totalBytes.value = file.size;
    uploadFileName.value = file.name;
    uploadStartTime.value = Date.now();
    lastUploadedBytes.value = 0;
    lastSpeedUpdateTime.value = Date.now();

    // 进度监听
    xhr.upload.addEventListener("progress", (event) => {
      if (event.lengthComputable) {
        uploadedBytes.value = event.loaded;
        totalBytes.value = event.total;
        uploadProgress.value = (event.loaded / event.total) * 100;

        // 计算速度（每 200ms 更新一次）
        const now = Date.now();
        const timeDiff = now - lastSpeedUpdateTime.value;
        if (timeDiff >= 200) {
          const bytesDiff = event.loaded - lastUploadedBytes.value;
          uploadSpeed.value = (bytesDiff / timeDiff) * 1000; // bytes per second
          lastUploadedBytes.value = event.loaded;
          lastSpeedUpdateTime.value = now;
        }
      }
    });

    xhr.addEventListener("load", () => {
      isUploading.value = false;
      if (xhr.status >= 200 && xhr.status < 300) {
        resolve();
      } else {
        reject(new Error(`Upload failed: ${xhr.statusText}`));
      }
    });

    xhr.addEventListener("error", () => {
      isUploading.value = false;
      reject(new Error("Upload failed: Network error"));
    });

    xhr.addEventListener("abort", () => {
      isUploading.value = false;
      reject(new Error("Upload aborted"));
    });

    xhr.open("POST", `${baseURL}/api/resources${path}?override=true`);
    xhr.setRequestHeader("X-Auth", authStore.jwt);
    xhr.send(file);
  });
};

const updateDiceYaml = async () => {
  try {
    // 读取 dice.yaml
    const response = await fetchURL("/api/resources/sealdice/data/dice.yaml", {});
    const fileInfo = await response.json();
    let content = fileInfo.content || "";

    // 检查并修改 serveAddress
    if (content.includes("serveAddress:")) {
      // 使用正则替换 serveAddress
      content = content.replace(
        /serveAddress:\s*['"]?[^'"\n]+['"]?/,
        'serveAddress: "0.0.0.0:3211"'
      );
    } else {
      // 如果没有 serveAddress，添加一个
      content = `serveAddress: "0.0.0.0:3211"\n${content}`;
    }

    // 保存修改后的文件
    await fetchURL("/api/resources/sealdice/data/dice.yaml", {
      method: "PUT",
      body: content,
    });
  } catch (e) {
    console.warn("Failed to update dice.yaml:", e);
    // 不抛出错误，因为文件可能不存在
  }
};

const downloadLatestBackup = async () => {
  isDownloading.value = true;

  try {
    // 获取 backups 目录的文件列表
    const response = await fetchURL("/api/resources/sealdice/backups/", {});
    const data = await response.json();

    if (!data.items || data.items.length === 0) {
      $showError(t("seal.noBackupsFound"));
      return;
    }

    // 过滤出备份文件 (bak_*.zip)
    const backupFiles = data.items.filter(
      (item: any) => !item.isDir && item.name.startsWith("bak_") && item.name.endsWith(".zip")
    );

    if (backupFiles.length === 0) {
      $showError(t("seal.noBackupsFound"));
      return;
    }

    // 按修改时间排序，获取最新的
    backupFiles.sort(
      (a: any, b: any) => new Date(b.modified).getTime() - new Date(a.modified).getTime()
    );

    const latestBackup = backupFiles[0];

    // 下载最新备份
    api.download(null, `/files/sealdice/backups/${latestBackup.name}`);
  } catch (error: any) {
    console.error("Download backup error:", error);
    $showError(error);
  } finally {
    isDownloading.value = false;
  }
};

const viewBackups = () => {
  router.push("/files/sealdice/backups/");
};
</script>

<style scoped>
/* 折叠按钮 */
.seal-toggle-btn {
  position: fixed;
  bottom: 1em;
  left: 1em;
  z-index: 101;
  width: 48px;
  height: 48px;
  border-radius: 50%;
  border: none;
  background: var(--blue);
  color: white;
  cursor: pointer;
  display: flex;
  align-items: center;
  justify-content: center;
  box-shadow: 0 2px 8px rgba(0, 0, 0, 0.2);
  transition: transform 0.2s ease, opacity 0.2s ease;
}

.seal-toggle-btn:hover {
  background: var(--blueDark);
}

.seal-toggle-btn i {
  font-size: 24px;
}

.seal-toggle-btn .seal-icon {
  width: 28px;
  height: 28px;
  object-fit: contain;
}

.seal-toggle-btn.collapsed {
  opacity: 0.9;
}

#seal-toolbar {
  position: fixed;
  bottom: 1em;
  left: 50%;
  transform: translateX(-50%);
  z-index: 100;
  transition: opacity 0.2s ease, transform 0.2s ease;
}

#seal-toolbar.hidden {
  opacity: 0;
  pointer-events: none;
  transform: translateX(-50%) translateY(100%);
}

#seal-toolbar.collapsed {
  opacity: 0;
  pointer-events: none;
  transform: translateY(100%);
}

.seal-toolbar-content {
  display: flex;
  align-items: center;
  background: var(--surfaceSecondary);
  border-radius: 8px;
  padding: 0.5em 1em;
  box-shadow: 0 2px 8px rgba(0, 0, 0, 0.15);
  gap: 1em;
  flex-wrap: nowrap;
}

.seal-toolbar-title {
  font-weight: 500;
  color: var(--textPrimary);
  white-space: nowrap;
  padding-right: 0.5em;
  border-right: 1px solid var(--borderPrimary);
  flex-shrink: 0;
}

.seal-toolbar-actions {
  display: flex;
  gap: 0.5em;
  flex-shrink: 0;
}

.seal-action {
  display: flex;
  align-items: center;
  gap: 0.4em;
  padding: 0.4em 0.8em;
  border: none;
  background: var(--blue);
  color: white;
  border-radius: 4px;
  cursor: pointer;
  font-size: 0.9em;
  transition: background 0.2s ease;
  white-space: nowrap;
  flex-shrink: 0;
}

.seal-action:hover:not(:disabled) {
  background: var(--blueDark);
}

.seal-action:disabled {
  opacity: 0.6;
  cursor: not-allowed;
}

.seal-action i {
  font-size: 1.2em;
  flex-shrink: 0;
}

.seal-action span {
  white-space: nowrap;
}

/* 警告弹框样式 */
.seal-warning-overlay {
  position: fixed;
  top: 0;
  left: 0;
  right: 0;
  bottom: 0;
  background: rgba(0, 0, 0, 0.5);
  z-index: 1000;
  display: flex;
  align-items: center;
  justify-content: center;
}

.seal-warning-dialog {
  background: var(--surfaceSecondary);
  border-radius: 8px;
  max-width: 480px;
  width: 90%;
  box-shadow: 0 4px 20px rgba(0, 0, 0, 0.3);
}

.seal-warning-header {
  display: flex;
  align-items: center;
  gap: 0.5em;
  padding: 1em;
  border-bottom: 1px solid var(--borderPrimary);
  font-weight: 600;
  font-size: 1.1em;
  color: var(--textPrimary);
}

.seal-warning-header .warning-icon {
  color: #f59e0b;
  font-size: 1.5em;
}

.seal-warning-content {
  padding: 1em;
  color: var(--textPrimary);
}

.seal-warning-content p {
  margin-bottom: 0.8em;
}

.seal-warning-content ul {
  margin: 0;
  padding-left: 1.5em;
}

.seal-warning-content li {
  margin-bottom: 0.5em;
  color: var(--textSecondary);
}

.seal-warning-disclaimer {
  margin-top: 1em;
  padding: 0.5em;
  background: rgba(220, 38, 38, 0.1);
  border-left: 3px solid #dc2626;
  color: #dc2626;
  font-weight: 500;
}

.seal-warning-actions {
  display: flex;
  justify-content: flex-end;
  gap: 0.5em;
  padding: 1em;
  border-top: 1px solid var(--borderPrimary);
}

.seal-warning-btn {
  padding: 0.5em 1.2em;
  border: none;
  border-radius: 4px;
  cursor: pointer;
  font-size: 0.9em;
  transition: background 0.2s ease;
}

.seal-warning-btn.cancel {
  background: var(--surfacePrimary);
  color: var(--textPrimary);
  border: 1px solid var(--borderPrimary);
}

.seal-warning-btn.cancel:hover {
  background: var(--surfaceSecondary);
}

.seal-warning-btn.confirm {
  background: #dc2626;
  color: white;
}

.seal-warning-btn.confirm:hover {
  background: #b91c1c;
}

.seal-warning-actions.three-buttons {
  flex-wrap: wrap;
  gap: 0.5em;
}

.seal-warning-actions.three-buttons .seal-warning-btn {
  flex: 1;
  min-width: 100px;
}

.seal-existing-backup-info {
  margin-top: 1em;
  padding: 0.8em;
  background: var(--surfacePrimary);
  border-radius: 4px;
  font-size: 0.9em;
}

.seal-existing-backup-info div {
  margin-bottom: 0.3em;
}

.seal-existing-backup-info div:last-child {
  margin-bottom: 0;
}

/* 上传进度弹窗样式 */
.seal-upload-overlay {
  position: fixed;
  top: 0;
  left: 0;
  right: 0;
  bottom: 0;
  background: rgba(0, 0, 0, 0.5);
  z-index: 1001;
  display: flex;
  align-items: center;
  justify-content: center;
}

.seal-upload-dialog {
  background: var(--surfaceSecondary);
  border-radius: 8px;
  max-width: 400px;
  width: 90%;
  box-shadow: 0 4px 20px rgba(0, 0, 0, 0.3);
}

.seal-upload-header {
  display: flex;
  align-items: center;
  gap: 0.5em;
  padding: 1em;
  border-bottom: 1px solid var(--borderPrimary);
  font-weight: 600;
  font-size: 1.1em;
  color: var(--textPrimary);
}

.seal-upload-header i {
  color: var(--blue);
  font-size: 1.5em;
}

.seal-upload-content {
  padding: 1.5em;
}

.seal-upload-filename {
  font-weight: 500;
  color: var(--textPrimary);
  margin-bottom: 1em;
  word-break: break-all;
  font-size: 0.95em;
}

.seal-upload-progress-bar {
  height: 8px;
  background: var(--surfacePrimary);
  border-radius: 4px;
  overflow: hidden;
  margin-bottom: 1em;
}

.seal-upload-progress-fill {
  height: 100%;
  background: linear-gradient(90deg, var(--blue), #60a5fa);
  border-radius: 4px;
  transition: width 0.1s ease;
}

.seal-upload-stats {
  display: flex;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 0.5em;
  font-size: 0.85em;
  color: var(--textSecondary);
}

.seal-upload-percent {
  font-weight: 600;
  color: var(--blue);
}

.seal-upload-speed {
  font-weight: 500;
  color: var(--textPrimary);
}

.seal-upload-size {
  color: var(--textSecondary);
}

/* 移动端样式 */
@media (max-width: 736px) {
  .seal-toggle-btn {
    bottom: 1em;
    left: 1em;
  }

  #seal-toolbar {
    bottom: 4.5em;
    width: calc(100% - 2em);
    max-width: none;
    left: 1em;
    right: 1em;
    transform: none;
  }

  #seal-toolbar.collapsed {
    transform: translateY(20px);
  }

  .seal-toolbar-content {
    flex-wrap: wrap;
    justify-content: center;
    padding: 0.6em;
    gap: 0.5em;
    border-radius: 8px;
  }

  .seal-toolbar-title {
    width: 100%;
    text-align: center;
    padding-right: 0;
    border-right: none;
    border-bottom: 1px solid var(--borderPrimary);
    padding-bottom: 0.5em;
  }

  .seal-toolbar-actions {
    width: 100%;
    justify-content: center;
    flex-wrap: wrap;
  }

  .seal-action {
    flex: 1;
    min-width: auto;
    justify-content: center;
    padding: 0.5em;
    font-size: 0.8em;
  }

  .seal-action span {
    display: inline;
  }
}
</style>
