// ===== 移动端导航切换 =====
const navToggle = document.getElementById('navToggle');
const navLinks = document.getElementById('navLinks');

if (navToggle && navLinks) {
    navToggle.addEventListener('click', () => {
        navLinks.classList.toggle('open');
    });
}

// ===== 小程序码弹窗 =====
const modal = document.getElementById('qrModal');
const openBtns = [
    document.getElementById('qrModalBtn'),
    document.getElementById('qrModalFooterBtn')
];
const closeBtn = document.getElementById('qrModalClose');

function openModal() {
    modal.classList.add('active');
    document.body.style.overflow = 'hidden';
}

function closeModal() {
    modal.classList.remove('active');
    document.body.style.overflow = '';
}

openBtns.forEach(btn => {
    if (btn) {
        btn.addEventListener('click', openModal);
    }
});

if (closeBtn) {
    closeBtn.addEventListener('click', closeModal);
}

modal.addEventListener('click', (e) => {
    if (e.target === modal) {
        closeModal();
    }
});

// ESC 键关闭弹窗
document.addEventListener('keydown', (e) => {
    if (e.key === 'Escape' && modal.classList.contains('active')) {
        closeModal();
    }
});

// ===== 平滑滚动（兼容 Safari） =====
document.querySelectorAll('a[href^="#"]').forEach(anchor => {
    anchor.addEventListener('click', function (e) {
        const href = this.getAttribute('href');
        if (href === '#') return;
        e.preventDefault();
        const target = document.querySelector(href);
        if (target) {
            target.scrollIntoView({ behavior: 'smooth' });
            // 关闭移动端菜单
            if (navLinks) navLinks.classList.remove('open');
        }
    });
});

console.log('📸 拍瞬传 · 项目主页加载完成');