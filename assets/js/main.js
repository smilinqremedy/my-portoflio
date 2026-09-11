// Ocheche Fredrick Portfolio - Interactive Interactions

document.addEventListener('DOMContentLoaded', () => {
  // 1. Tab Switching (What I Do / Ongoing Projects / Tech Stack)
  const tabTriggers = document.querySelectorAll('.tab-trigger-btn');
  const tabPanels = document.querySelectorAll('.tab-content-panel');

  tabTriggers.forEach(btn => {
    btn.addEventListener('click', () => {
      const targetId = btn.getAttribute('data-tab');

      // Update active states
      tabTriggers.forEach(b => b.classList.remove('active'));
      tabPanels.forEach(p => p.classList.remove('active'));

      btn.classList.add('active');
      const targetPanel = document.getElementById(targetId);
      if (targetPanel) {
        targetPanel.classList.add('active');
      }
    });
  });

  // 2. Mobile Navigation Toggle
  const mobileBtn = document.getElementById('mobile-nav-toggle');
  const navMenu = document.getElementById('navbar-links');

  if (mobileBtn && navMenu) {
    mobileBtn.addEventListener('click', () => {
      navMenu.classList.toggle('open');
      const isOpen = navMenu.classList.contains('open');
      mobileBtn.textContent = isOpen ? '✕' : '☰';
    });

    navMenu.querySelectorAll('a').forEach(link => {
      link.addEventListener('click', () => {
        navMenu.classList.remove('open');
        mobileBtn.textContent = '☰';
      });
    });
  }
});
