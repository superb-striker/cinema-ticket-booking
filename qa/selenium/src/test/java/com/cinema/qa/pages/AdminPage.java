package com.cinema.qa.pages;

import com.cinema.qa.support.BrowserSession;
import org.openqa.selenium.By;
import org.openqa.selenium.WebDriver;

public final class AdminPage extends BasePage {
    public AdminPage(WebDriver driver) { super(driver); }

    public AdminPage open() {
        driver.get(BrowserSession.baseUrl() + "/admin.html");
        wait.until(d -> !d.findElements(By.id("accessDenied")).isEmpty());
        return this;
    }

    public void awaitAccessDenied() {
        visible(By.cssSelector("#accessDenied:not(.hidden)"));
        wait.until(d -> d.findElements(By.cssSelector("[data-admin-only]:not(.hidden)")).isEmpty());
    }

    public String denialText() { return driver.findElement(By.id("accessDenied")).getText(); }
}
