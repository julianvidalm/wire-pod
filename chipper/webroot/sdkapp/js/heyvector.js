async function triggerHeyVector() {
  const statusDiv = document.getElementById("heyVectorStatus");
  statusDiv.innerHTML = "<p>Waking the robot...</p>";
  
  try {
    const response = await fetch("/api-sdk/trigger_wake_word?serial=" + esn, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/x-www-form-urlencoded'
      }
    });
    
    if (!response.ok) {
      throw new Error("Failed to trigger wake word");
    }
    
    const result = await response.text();
    
    if (result.includes("success") || result.includes("ok")) {
      statusDiv.innerHTML = "<p>The robot is listening.</p>";
    } else {
      throw new Error(result || "Unknown error");
    }
    
    setTimeout(() => {
      statusDiv.innerHTML = "";
    }, 5000);
    
  } catch (error) {
    console.error("Error triggering Hey Vector:", error);
    statusDiv.innerHTML = "<p>Could not wake the robot: " + error.message + "</p>";
    
    setTimeout(() => {
      statusDiv.innerHTML = "";
    }, 5000);
  }
}
