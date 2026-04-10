import { Events } from '@wailsio/runtime';
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
    GuessCategory 
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
const newRepoName = document.getElementById('new-repo-name');
const newRepoUrl = document.getElementById('new-repo-url');
const btnAddRepo = document.getElementById('btn-add-repo');
const autostartToggle = document.getElementById('autostart-toggle');
const historyList = document.getElementById('history-list');
const btnSettingsBack = document.getElementById('btn-settings-back');

// NEW: Queue system to handle rapid downloads or multi-file drops
let fileQueue = [];
let currentConfig = null;

// Safe Wails initialization to prevent blank screens
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

// Queue Processing Engine
async function processNextInQueue() {
    if (fileQueue.length === 0) {
        resetStandbyUI();
        await HideWindow();
        return;
    }

    let currentFilename = fileQueue[0];
    const normalizedPath = currentFilename.replace(/\\/g, '/');
    const baseName = normalizedPath.split('/').pop();
    
    mainTitle.innerText = "New KiCad Part Detected";
    filenameDisplay.innerHTML = `<strong>${baseName}</strong><br><small style="color: var(--text-muted);">Analyzing contents...</small>`;
    
    // Show queue badge if multiple files exist
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

    // Auto Category Guessing
    try {
        const guessedCategory = await GuessCategory(currentFilename);
        if (guessedCategory) {
            const options = Array.from(selectCategory.options).map(opt => opt.value);
            if (options.includes(guessedCategory)) {
                selectCategory.value = guessedCategory;
            }
        }
    } catch (err) {}

    // File Summary
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

// Wails V3 EVENT LISTENERS
Events.On("file-detected", async (e) => {
    const filename = Array.isArray(e.data) ? e.data[0] : e.data;
    fileQueue.push(filename);
    if (fileQueue.length === 1) {
        // First item — start processing immediately
        await processNextInQueue();
    }
    // Additional items are already shown via the queue badge inside processNextInQueue
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

// REMOVED manual DOM event listeners:
// Wails v3 intercepts OS file drags natively when EnableFileDrop is true.
// The file path goes directly to Go, which triggers HandleDroppedItem and emits "file-detected"

autostartToggle.addEventListener('change', async (e) => {
    // Note: ToggleAutoStart is not implemented in app.go currently.
    alert("AutoStart configuration is currently managed via your OS settings.");
    e.target.checked = !e.target.checked; 
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
        await loadConfig();
        selectCategory.value = chosenCategory; // Remembers your choice for the next item in queue
        
        fileQueue.shift(); // Remove the finished file
        await processNextInQueue(); // Load the next one
    } catch (err) {
        alert("Processing Error: " + err);
        btnOk.disabled = false;
    }
});

document.getElementById('btn-skip').addEventListener('click', async () => {
    if (fileQueue.length > 0) {
        await SkipFile(fileQueue[0]);
        fileQueue.shift(); // Pop the skipped file
        await processNextInQueue(); // Show next
    }
});

document.getElementById('btn-cancel').addEventListener('click', async () => {
    fileQueue = []; // Clear the entire queue on cancel
    await HideWindow();
    resetStandbyUI(); 
});

btnSettings.addEventListener('click', () => {
    switchView(settingsView);
});

btnSettingsBack.addEventListener('click', () => {
    switchView(mainView);
});

btnAddRepo.addEventListener('click', async () => {
    const name = newRepoName.value.trim();
    const url = newRepoUrl.value.trim();
    
    if (!name) {
        alert("Please provide a folder name for the repository.");
        return;
    }

    const originalText = btnAddRepo.innerText;
    btnAddRepo.disabled = true;
    btnAddRepo.innerText = url ? "Cloning Git Repository..." : "Creating Folder...";

    try {
        await AddRepository(name, url);
        await loadConfig();
        newRepoName.value = "";
        newRepoUrl.value = "";
    } catch (err) {
        alert("Failed to add repository:\n" + err);
    } finally {
        btnAddRepo.disabled = false;
        btnAddRepo.innerText = originalText;
    }
});