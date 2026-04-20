import { Events, Call } from '@wailsio/runtime';
import {
    GetConfig,
    ProcessFile,
    UndoAction,
    SelectDirectory,
    SelectWatchDirectory,
    SaveSetup,
    AddRepository,
    SkipFile,
    HandleDroppedItem,
    HideWindow,
    GetItemSummary,
    GuessCategory,
    ToggleAutoStart
} from '../bindings/kicad-lib-mgr/app.js';

const setupView = document.getElementById('setup-view');
const mainView = document.getElementById('main-view');
const settingsView = document.getElementById('settings-view');

const libPathInput = document.getElementById('lib-path-input');
const btnBrowse = document.getElementById('btn-browse');
const btnSaveSetup = document.getElementById('btn-save-setup');

const mainTitle = document.getElementById('main-title');
const filenameDisplay = document.getElementById('filename-display');
const repositorySelect = document.getElementById('repository-select');
const selectCategory = document.getElementById('category-select');
const newCategoryInput = document.getElementById('new-category-input');
const btnSettings = document.getElementById('btn-settings');
const btnOk = document.getElementById('btn-ok');

const watchDirInput = document.getElementById('watch-dir-input');
const btnBrowseWatch = document.getElementById('btn-browse-watch');
const repoList = document.getElementById('repo-list');
const syncStatusIcon = document.getElementById('sync-status-icon');
const btnSyncNow = document.getElementById('btn-sync-now');
const autostartToggle = document.getElementById('autostart-toggle');
const historyList = document.getElementById('history-list');
const btnSettingsBack = document.getElementById('btn-settings-back');

// UI Toggle Elements (New)
const navTabLibs = document.getElementById('nav-tab-libs');
const navTabSys = document.getElementById('nav-tab-sys');
const contentLibs = document.getElementById('content-libs');
const contentSys = document.getElementById('content-sys');
const btnShowAddRepo = document.getElementById('btn-show-add-repo');
const addRepoContainer = document.getElementById('add-repo-container');
const tabLocal = document.getElementById('tab-local');
const tabGit = document.getElementById('tab-git');
const formLocal = document.getElementById('form-local');
const formGit = document.getElementById('form-git');

// Form Inputs (Existing logic hooks here)
const newLocalName = document.getElementById('new-local-name');
const btnAddLocal = document.getElementById('btn-add-local');
const newGitUrl = document.getElementById('new-git-url');
const newGitName = document.getElementById('new-git-name');
const btnAddGit = document.getElementById('btn-add-git');

// Queue system to handle rapid downloads or multi-file drops
let fileQueue = [];
let currentConfig = null;

document.addEventListener("DOMContentLoaded", () => {
    setTimeout(() => {
        loadConfig().catch(err => {
            console.error("Critical initialization failure:", err);
            document.body.innerHTML = `<h2 style="color:#ff5555; padding:20px;">Startup Error: Failed to connect to background service.</h2>`;
        });
    }, 100);
});

async function loadConfig() {
    try {
        currentConfig = await GetConfig();
        
        if (!currentConfig.baseLibPath) {
            switchView(setupView);
        } else {
            if (settingsView.classList.contains('hidden') && fileQueue.length === 0) {
                switchView(mainView);
            }
            watchDirInput.value = currentConfig.watchDir || "";
            autostartToggle.checked = currentConfig.autoStart || false;
            populateCategories(currentConfig.categories || []);
            populateRepositories(currentConfig.repositories || []);
            populateHistory(currentConfig.history || []);
        }
    } catch (err) {
        console.error("Failed to load config:", err);
    }
}

function switchView(activeView) {
    [setupView, mainView, settingsView].forEach(view => view.classList.add('hidden'));
    activeView.classList.remove('hidden');
}

function populateCategories(categories) {
    const previousSelection = selectCategory.value;
    selectCategory.innerHTML = "";
    
    [...categories].sort((a, b) => a.toLowerCase().localeCompare(b.toLowerCase())).forEach(cat => {
        let opt = document.createElement('option');
        opt.value = cat;
        opt.innerText = cat;
        selectCategory.appendChild(opt);
    });

    let optNew = document.createElement('option');
    optNew.value = "ADD_NEW";
    optNew.innerText = "+ Add New Category...";
    selectCategory.appendChild(optNew);

    if (previousSelection && previousSelection !== "ADD_NEW") {
        selectCategory.value = previousSelection;
    }
}

function populateRepositories(repositories) {
    const previousRepo = repositorySelect.value;
    repositorySelect.innerHTML = "";
    repoList.innerHTML = ""; 

    repositories.forEach(repo => {
        let opt = document.createElement('option');
        opt.value = repo.name;
        opt.innerText = !repo.url ? `${repo.name} (Local Folder)` : repo.name;
        repositorySelect.appendChild(opt);

        let li = document.createElement('li');
        li.style.display = "flex";
        li.style.justifyContent = "space-between";
        li.style.alignItems = "center";
        
        let info = document.createElement('div');
        info.innerHTML = `<strong>${repo.name}</strong> <span style="color: var(--text-muted); font-size: 0.8rem;">${repo.url ? '(' + repo.url + ')' : '(Local)'}</span>`;
        
        li.appendChild(info);
        repoList.appendChild(li);
    });

    if (previousRepo) repositorySelect.value = previousRepo;
}

function populateHistory(historyItems) {
    historyList.innerHTML = "";
    if (!historyItems || historyItems.length === 0) {
        historyList.innerHTML = "<li class='history-item'><span class='history-text' style='color: var(--text-muted);'>No recent imports.</span></li>";
        return;
    }

    const reversed = [...historyItems].reverse();

    reversed.forEach(item => {
        let li = document.createElement('li');
        li.className = 'history-item';
        
        let txt = document.createElement('span');
        txt.className = 'history-text';
        txt.innerText = `${item.filename} → ${item.category}`;
        txt.title = `Imported to ${item.repoName} on ${new Date(item.timestamp * 1000).toLocaleString()}`;

        let btn = document.createElement('button');
        btn.className = 'btn-undo';
        btn.innerText = 'Undo';
        btn.onclick = async () => {
            if (confirm(`Are you sure you want to rollback the import of ${item.filename}?`)) {
                const success = await UndoAction(item.id);
                if (success) {
                    await loadConfig();
                } else {
                    alert("Undo failed. The files may have been moved or deleted manually.");
                }
            }
        };

        li.appendChild(txt);
        li.appendChild(btn);
        historyList.appendChild(li);
    });
}

function resetStandbyUI() {
    mainTitle.innerText = "Monitoring Folder...";
    filenameDisplay.innerHTML = "Waiting for file...";
    
    newCategoryInput.value = "";
    newCategoryInput.classList.add('hidden');
    
    btnOk.disabled = true;
    btnOk.style.opacity = "0.5";
    btnOk.style.cursor = "not-allowed";
}

async function processNextInQueue() {
    if (fileQueue.length === 0) {
        resetStandbyUI();
        await HideWindow();
        return;
    }

    let currentFilename = fileQueue[0];
    const baseName = currentFilename.split(/[\\/]/).pop();
    
    mainTitle.innerText = "New KiCad Part Detected";
    filenameDisplay.innerHTML = `<strong>${baseName}</strong><br><small style="color: var(--text-muted);">Analyzing contents...</small>`;
    
    if (fileQueue.length > 1) {
        filenameDisplay.innerHTML += `<br><span style="color: var(--accent); font-weight: bold; font-size: 0.85rem; display: inline-block; margin-top: 4px;">Queue: ${fileQueue.length - 1} more file(s) waiting...</span>`;
    }
    
    await loadConfig();
    switchView(mainView);
    
    newCategoryInput.classList.add('hidden');
    newCategoryInput.value = "";
    
    btnOk.disabled = false;
    btnOk.style.opacity = "1";
    btnOk.style.cursor = "pointer";

    try {
        const guessedCategory = await GuessCategory(currentFilename);
        if (guessedCategory) {
            const options = Array.from(selectCategory.options).map(opt => opt.value);
            if (options.includes(guessedCategory)) {
                selectCategory.value = guessedCategory;
            }
        }
    } catch (err) {}

    try {
        const summary = await GetItemSummary(currentFilename);
        filenameDisplay.innerHTML = `<strong>${baseName}</strong><br><small style="color: var(--accent); opacity: 0.9;">${summary}</small>`;
        if (fileQueue.length > 1) {
            filenameDisplay.innerHTML += `<br><span style="color: var(--accent); font-weight: bold; font-size: 0.85rem; display: inline-block; margin-top: 4px;">Queue: ${fileQueue.length - 1} more file(s) waiting...</span>`;
        }
    } catch (err) {
        filenameDisplay.innerHTML = `<strong>${baseName}</strong>`;
    }
}

// UI Toggles (Tabs & Expanders)
navTabLibs.addEventListener('click', () => {
    navTabLibs.classList.add('active');
    navTabSys.classList.remove('active');
    contentLibs.classList.add('active');
    contentSys.classList.remove('active');
});

navTabSys.addEventListener('click', () => {
    navTabSys.classList.add('active');
    navTabLibs.classList.remove('active');
    contentSys.classList.add('active');
    contentLibs.classList.remove('active');
});

btnShowAddRepo.addEventListener('click', () => {
    addRepoContainer.classList.toggle('hidden');
    const isHidden = addRepoContainer.classList.contains('hidden');
    btnShowAddRepo.innerText = isHidden ? "+ Add Repo" : "Collapse";
});

tabLocal.addEventListener('click', () => {
    tabLocal.classList.add('active');
    tabGit.classList.remove('active');
    formLocal.classList.remove('hidden');
    formGit.classList.add('hidden');
});

tabGit.addEventListener('click', () => {
    tabGit.classList.add('active');
    tabLocal.classList.remove('active');
    formGit.classList.remove('hidden');
    formLocal.classList.add('hidden');
});

// Logic Event Listeners
Events.On("file-detected", async (e) => {
    const filename = Array.isArray(e.data) ? e.data[0] : e.data;
    fileQueue.push(filename);
    if (fileQueue.length === 1) {
        await processNextInQueue();
    }
});

Events.On("watcher-error", (e) => {
    const message = Array.isArray(e.data) ? e.data[0] : e.data;
    console.error("Watcher error:", message);
    const titleEl = document.getElementById('main-title');
    if (titleEl) {
        titleEl.innerText = "Watch Error";
        titleEl.style.color = "var(--error, #ff5555)";
    }
    const filenameEl = document.getElementById('filename-display');
    if (filenameEl) {
        filenameEl.innerHTML = `<small style="color: var(--error, #ff5555);">${message}<br>Check the watch folder in Settings.</small>`;
    }
});

Events.On("file-rejected", (e) => {
    const filename = Array.isArray(e.data) ? e.data[0] : e.data;
    const baseName = filename.split(/[\\/]/).pop();
    const overlay = document.getElementById('drag-overlay');
    const overlayText = overlay.querySelector('div');
    
    const originalText = overlayText.innerText;
    overlayText.innerText = `❌ No KiCad assets in: ${baseName}`;
    overlayText.style.color = '#ff5555';
    overlay.style.borderColor = '#ff5555';
    
    overlay.style.display = 'flex';
    document.body.classList.add('shake-animation');
    
    setTimeout(() => {
        overlay.style.display = '';
        overlayText.innerText = originalText;
        overlayText.style.color = '';
        overlay.style.borderColor = '';
        document.body.classList.remove('shake-animation');
    }, 2500);
});

autostartToggle.addEventListener('change', async (e) => {
    try {
        await ToggleAutoStart(e.target.checked);
    } catch (err) {
        console.error('AutoStart toggle failed:', err);
        e.target.checked = !e.target.checked; 
    }
});

btnBrowse.addEventListener('click', async () => {
    const selectedDir = await SelectDirectory();
    if (selectedDir) {
        libPathInput.value = selectedDir;
        btnSaveSetup.disabled = false;
    }
});

btnSaveSetup.addEventListener('click', async () => {
    const path = libPathInput.value;
    if (path) {
        try {
            await SaveSetup(path);
            await loadConfig();
            if (fileQueue.length === 0) await HideWindow();
        } catch (err) {
            alert("Failed to save setup: " + err);
        }
    }
});

btnBrowseWatch.addEventListener('click', async () => {
    const selectedDir = await SelectWatchDirectory();
    if (selectedDir) {
        watchDirInput.value = selectedDir;
        await loadConfig();
    }
});

selectCategory.addEventListener('change', (e) => {
    if (e.target.value === "ADD_NEW") {
        newCategoryInput.classList.remove('hidden');
        newCategoryInput.focus();
    } else {
        newCategoryInput.classList.add('hidden');
    }
});

btnOk.addEventListener('click', async () => {
    if (fileQueue.length === 0) return;

    let currentFilename = fileQueue[0];
    let chosenCategory = selectCategory.value;
    let chosenRepo = repositorySelect.value;
    
    if (chosenCategory === "ADD_NEW") {
        chosenCategory = newCategoryInput.value.trim();
        if (chosenCategory === "") {
            newCategoryInput.style.borderColor = "red";
            setTimeout(() => newCategoryInput.style.borderColor = "", 2000);
            return;
        }
    }
    
    btnOk.disabled = true;
    
    try {
        await ProcessFile(currentFilename, chosenCategory, chosenRepo);
        fileQueue.shift(); 
        await loadConfig();
        selectCategory.value = chosenCategory; 
        await processNextInQueue(); 
    } catch (err) {
        alert("Processing Error: " + err);
        btnOk.disabled = false;
    }
});

document.getElementById('btn-skip').addEventListener('click', async () => {
    if (fileQueue.length > 0) {
        await SkipFile(fileQueue[0]);
        fileQueue.shift(); 
        await processNextInQueue(); 
    }
});

document.getElementById('btn-cancel').addEventListener('click', async () => {
    fileQueue = []; 
    await HideWindow();
    resetStandbyUI(); 
});

btnSettings.addEventListener('click', () => {
    switchView(settingsView);
});

btnSettingsBack.addEventListener('click', () => {
    switchView(mainView);
});

btnAddLocal.addEventListener('click', async () => {
    const name = newLocalName.value.trim();
    if (!name) {
        newLocalName.style.borderColor = 'red';
        setTimeout(() => newLocalName.style.borderColor = '', 2000);
        return;
    }

    btnAddLocal.disabled = true;
    btnAddLocal.innerText = 'Creating folder...';
    try {
        await AddRepository(name, '');
        await loadConfig();
        newLocalName.value = '';
        addRepoContainer.classList.add('hidden');
        btnShowAddRepo.innerText = "+ Add Repo";
    } catch (err) {
        alert('Failed to create library:\n' + err);
    } finally {
        btnAddLocal.disabled = false;
        btnAddLocal.innerText = 'Create Local Library';
    }
});

btnAddGit.addEventListener('click', async () => {
    const url = newGitUrl.value.trim();
    const name = newGitName.value.trim();

    if (!url) {
        newGitUrl.style.borderColor = 'red';
        setTimeout(() => newGitUrl.style.borderColor = '', 2000);
        return;
    }
    if (!name) {
        newGitName.style.borderColor = 'red';
        setTimeout(() => newGitName.style.borderColor = '', 2000);
        return;
    }

    btnAddGit.disabled = true;
    btnAddGit.innerText = 'Validating URL...';
    syncStatusIcon.title = 'Validating repository URL...';

    try {
        btnAddGit.innerText = 'Cloning repository...';
        await AddRepository(name, url);
        await loadConfig();
        newGitUrl.value = '';
        newGitName.value = '';
        addRepoContainer.classList.add('hidden');
        btnShowAddRepo.innerText = "+ Add Repo";
    } catch (err) {
        alert('Failed to connect library:\n' + err);
    } finally {
        btnAddGit.disabled = false;
        btnAddGit.innerText = 'Validate & Clone';
    }
});

btnSyncNow.addEventListener('click', async () => {
    btnSyncNow.disabled = true;
    try {
        await Call.ByName('App.SyncAllRepositories');
    } catch (err) {
        console.error('Sync failed:', err);
    } finally {
        btnSyncNow.disabled = false;
    }
});

Events.On('sync-status', (e) => {
    const state = Array.isArray(e.data) ? e.data[0] : e.data;
    syncStatusIcon.style.opacity = '1';
    switch (state) {
        case 'syncing':
            syncStatusIcon.innerText = '🔄';
            syncStatusIcon.title = 'Syncing...';
            break;
        case 'synced':
            syncStatusIcon.innerText = '✅';
            syncStatusIcon.title = 'All libraries up to date.';
            break;
        case 'warning':
            syncStatusIcon.innerText = '⚠️';
            syncStatusIcon.title = 'Offline or un-synced local changes.';
            break;
    }
});