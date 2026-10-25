package com.cinema.qa.support;

import io.github.bonigarcia.wdm.WebDriverManager;
import java.time.Duration;
import java.net.URI;
import org.openqa.selenium.WebDriver;
import org.openqa.selenium.chrome.ChromeDriver;
import org.openqa.selenium.chrome.ChromeOptions;
import org.openqa.selenium.remote.RemoteWebDriver;
import org.openqa.selenium.logging.LogType;
import org.openqa.selenium.logging.LoggingPreferences;
import java.util.logging.Level;

public final class BrowserSession {
    private BrowserSession() {}

    public static WebDriver create() {
        ChromeOptions options = new ChromeOptions();
        if (Boolean.parseBoolean(System.getProperty("headless", "true"))) {
            options.addArguments("--headless=new");
        }
        options.addArguments("--window-size=1440,1000", "--disable-dev-shm-usage");
        String binary = System.getProperty("chrome.binary", "");
        if (!binary.isBlank()) options.setBinary(binary);
        LoggingPreferences logs = new LoggingPreferences();
        logs.enable(LogType.BROWSER, Level.ALL);
        options.setCapability("goog:loggingPrefs", logs);
        WebDriver driver;
        String remoteUrl = System.getProperty("remoteUrl", "");
        if (remoteUrl.isBlank()) {
            WebDriverManager.chromedriver().setup();
            driver = new ChromeDriver(options);
        } else {
            try {
                driver = new RemoteWebDriver(URI.create(remoteUrl).toURL(), options);
            } catch (java.net.MalformedURLException error) {
                throw new IllegalArgumentException("Invalid remoteUrl", error);
            }
        }
        driver.manage().timeouts().implicitlyWait(Duration.ZERO);
        driver.manage().timeouts().pageLoadTimeout(Duration.ofSeconds(30));
        return driver;
    }

    public static String baseUrl() {
        return System.getProperty("baseUrl", "http://localhost:8080").replaceAll("/+$", "");
    }
}
