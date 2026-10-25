package com.cinema.qa.pages;

import com.cinema.qa.support.BrowserSession;
import org.openqa.selenium.By;
import org.openqa.selenium.WebDriver;

public final class ScannerPage extends BasePage {
    public ScannerPage(WebDriver driver) { super(driver); }

    public ScannerPage open() {
        driver.get(BrowserSession.baseUrl() + "/scanner.html");
        visible(By.id("manualForm"));
        return this;
    }

    public void enterKey(String key) { set(By.id("scannerKey"), key); }
    public void enterCode(String code) { set(By.id("ticketCode"), code); }
    public void submit() { clickable(By.cssSelector("#manualForm button[type='submit']")).click(); }

    public void awaitResult(String heading) {
        wait.until(d -> d.findElement(By.cssSelector("#result h2")).getText().equals(heading));
    }

    public String resultText() { return driver.findElement(By.id("result")).getText(); }
}
